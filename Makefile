PROJECT := thump

REGISTRY := ghcr.io/ianeff
BEATS    := clank rattle hiss thump

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse HEAD 2>/dev/null || echo none)
# Pinned to the commit's own timestamp, not wall-clock — two builds of the
# same commit must produce the same binary (and therefore the same image
# digest), or `images`/`sign-images` silently drift the :$(COMMIT) tag out
# from under whatever was already signed. Falls back to wall-clock only
# outside a git checkout (e.g. a source tarball with no .git).
DATE := $(shell git show -s --format=%cd --date=format:'%Y-%m-%dT%H:%M:%SZ' HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-s -w \
  -X main.version=$(VERSION) \
  -X main.commit=$(COMMIT) \
  -X main.date=$(DATE)"


.PHONY: all ci fmt fmt-check vet lint vulncheck chart-lint test race coverage build images push-images sign-images sbom-binaries sign-binaries run-clank run-rattle run-hiss run-thump tidy clean eval capture-detection

all: ci

ci: fmt-check vet lint vulncheck chart-lint race build

fmt:
	go fmt ./...

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; echo "run: make fmt"; exit 1; fi

vet:
	go vet ./...

lint:
	golangci-lint run

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

chart-lint:
	helm template deploy/chart/thump | go run github.com/yannh/kubeconform/cmd/kubeconform@v0.8.0 -strict -summary

test:
	go test ./...

race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# eval is key-gated (ANTHROPIC_API_KEY) and NEVER part of `ci` — a real-model
# assertion is exactly the flakiness make ci exists to keep out. Missing key =
# a clean skip, not a failure, so this is always safe to run.
eval:
	go test -tags eval ./internal/clank -run TestEval_ReasonerAgainstProductionCatalog -v

# capture-detection farms a live detection into a fixture:
#   make capture-detection SRC=/tmp/thump/detections/processed/slo_burn:ceph-cluster-....yaml NAME=my-fixture
capture-detection:
	@test -n "$(SRC)" && test -n "$(NAME)" || \
	  (echo "usage: make capture-detection SRC=<path to .yaml> NAME=<fixture-name>"; exit 1)
	cp "$(SRC)" internal/clank/testdata/detections/$(NAME).yaml
	@echo "captured internal/clank/testdata/detections/$(NAME).yaml"
	@echo "-> add a row to evalTable() in internal/clank/eval_test.go if it belongs in the eval score"

build:
	@mkdir -p bin
	go build -trimpath $(LDFLAGS) -o bin/clank ./cmd/clank
	go build -trimpath $(LDFLAGS) -o bin/rattle ./cmd/rattle
	go build -trimpath $(LDFLAGS) -o bin/hiss ./cmd/hiss
	go build -trimpath $(LDFLAGS) -o bin/thump ./cmd/thump

# images builds one container per beat, tagged with the git SHA (never
# `latest` — mutable tags break GitOps drift detection), with an SBOM and
# SLSA provenance attestation attached to the manifest list via buildx's
# own scanner — no separate syft invocation needed for the image path.
# Requires a buildx builder using the docker-container driver (the default
# docker driver can't emit multi-platform manifests or attestations).
# --metadata-file records the pushed manifest-list digest per beat — the
# thing sign-images actually signs, never the mutable tag (Liz Rice's rule:
# pin and verify by digest, a tag is a pointer, not an identity).
# Override the destination with `make images REGISTRY=ghcr.io/whoever`.
images:
	@mkdir -p bin/metadata
	@for beat in $(BEATS); do \
    	echo "building $(REGISTRY)/thump-$$beat:$(COMMIT) (linux/amd64,linux/arm64)"; \
   		docker buildx build \
   			--platform linux/amd64,linux/arm64 \
   			--sbom=true \
   			--provenance=true \
   			--build-arg BEAT=$$beat \
   			--build-arg VERSION=$(VERSION) \
   			--build-arg COMMIT=$(COMMIT) \
   			--build-arg DATE=$(DATE) \
   			-t $(REGISTRY)/thump-$$beat:$(COMMIT) \
   			--metadata-file bin/metadata/$$beat.json \
   			--push \
   			. || exit 1; \
	done

push-images: images
	@echo "multi-arch manifests already pushed by 'images' (buildx --push) — nothing more to do"

# sign-images keyless-signs (Sigstore/Fulcio OIDC, no long-lived key) the
# exact digest images just pushed, read back from its --metadata-file —
# never the :$(COMMIT) tag, which is only a pointer to that digest, not the
# digest itself. Doesn't touch the source tree or the pushed manifest;
# purely additive on the registry side. By default also submits a public
# Rekor transparency-log entry — decide on that deliberately, don't just
# discover it later. Fulcio is a shared public service outside this repo's
# control; 120s bounds the wait (OIDC login + cert issuance) so a stall
# there fails loud instead of hanging the terminal forever.
sign-images: images
	@for beat in $(BEATS); do \
		digest=$$(jq -r '."containerimage.digest"' bin/metadata/$$beat.json); \
		echo "signing $(REGISTRY)/thump-$$beat@$$digest"; \
		timeout 120 go run github.com/sigstore/cosign/v2/cmd/cosign@latest sign --yes \
			$(REGISTRY)/thump-$$beat@$$digest || exit 1; \
	done

# sbom-binaries/sign-binaries cover the non-container path: bin/ output
# isn't shipped anywhere today (images is the real delivery surface), kept
# here so the tooling exists once thump grows a bare-binary release channel.
sbom-binaries: build
	@mkdir -p bin/sbom
	@for beat in $(BEATS); do \
		go run github.com/anchore/syft/cmd/syft@latest bin/$$beat -o spdx-json=bin/sbom/$$beat.sbom.json; \
	done

sign-binaries: sbom-binaries
	@for beat in $(BEATS); do \
		go run github.com/sigstore/cosign/v2/cmd/cosign@latest sign-blob --yes \
			--output-signature=bin/sbom/$$beat.sig \
			--output-certificate=bin/sbom/$$beat.pem \
			bin/$$beat; \
	done

run-clank:
	go run ./cmd/clank

run-rattle:
	go run ./cmd/rattle

run-hiss:
	go run ./cmd/hiss

run-thump:
	go run ./cmd/thump

tidy:
	go mod tidy

clean:
	rm -rf bin/ coverage.out
