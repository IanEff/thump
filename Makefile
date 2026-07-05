PROJECT := thump

REGISTRY := ghcr.io/ianeff
BEATS    := clank rattle hiss thump

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-s -w \
  -X main.version=$(VERSION) \
  -X main.commit=$(COMMIT) \
  -X main.date=$(DATE)"


.PHONY: all ci fmt fmt-check vet lint vulncheck test race coverage build images push-images run tidy clean eval capture-detection

all: ci

ci: fmt-check vet lint vulncheck race build

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
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

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
	go build $(LDFLAGS) -o bin/clank ./cmd/clank
	go build $(LDFLAGS) -o bin/rattle ./cmd/rattle
	go build $(LDFLAGS) -o bin/hiss ./cmd/hiss
	go build $(LDFLAGS) -o bin/thump ./cmd/thump

# images builds one container per beat, tagged with the git SHA (never
# `latest` — mutable tags break GitOps drift detection). Override the
# destination with `make images REGISTRY=ghcr.io/whoever`.
images:
	@for beat in $(BEATS); do \
		echo "building $(REGISTRY)/thump-$$beat:$(COMMIT)"; \
		docker build \
			--build-arg BEAT=$$beat \
			--build-arg VERSION=$(VERSION) \
			--build-arg COMMIT=$(COMMIT) \
			--build-arg DATE=$(DATE) \
			-t $(REGISTRY)/thump-$$beat:$(COMMIT) \
			. || exit 1; \
	done

push-images: images
	@for beat in $(BEATS); do \
		docker push $(REGISTRY)/thump-$$beat:$(COMMIT) || exit 1; \
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
