PROJECT := clank

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-s -w \
  -X main.version=$(VERSION) \
  -X main.commit=$(COMMIT) \
  -X main.date=$(DATE)"


.PHONY: all ci fmt fmt-check vet lint vulncheck test race coverage build run tidy clean

all: ci

ci: fmt-check vet lint test build

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

build:
	@mkdir -p bin
	go build $(LDFLAGS) -o bin/clank ./cmd/clank
	go build $(LDFLAGS) -o bin/rattle ./cmd/rattle
	go build $(LDFLAGS) -o bin/hiss ./cmd/hiss
	go build $(LDFLAGS) -o bin/thump ./cmd/thump

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
