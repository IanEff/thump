# Design Spec: Compile-Time OpenTelemetry Instrumentation (otelc)

**Date**: 2026-07-29  
**Status**: Approved  
**Author**: Antigravity & Ian Furst  

---

## 1. Executive Summary

This spec outlines the design for integrating OpenTelemetry's compile-time instrumentation (`otelc`, via `github.com/open-telemetry/opentelemetry-go-compile-instrumentation`) across all five `thump` beat binaries (`clank`, `hiss`, `rattle`, `thump`, `trim`).

The primary architectural constraint is **toolchain non-interference**: standard Go developer operations (`go test ./...`, `go build ./...`, `gopls` IDE support, `golangci-lint`, `task ci`, and clean checkout module downloads) must operate cleanly without requiring pre-generated uncommitted directories or failing due to missing `replace` targets in `go.mod`.

---

## 2. Architecture & Design Principles

```mermaid
flowchart TD
    subgraph Local Dev & CI (Default)
        A[Clean Source Repository] --> B[go test / gopls / golangci-lint]
        B --> C[Standard Execution - Zero magic]
    end

    subgraph Instrumented Build Pipeline
        D[task build:otelc / Docker / GoReleaser] --> E[go generate ./otel.instrumentation.go]
        E --> F[otelc pin --generate creates .otelc-build/]
        F --> G[go tool otelc go build]
        G --> H[Instrumented Binary Output]
    end
```

### Key Invariants

1. **Clean `go.mod` in Source Control**:
   - `go.mod` in Git will contain `tool go.opentelemetry.io/otelc/tool/cmd/otelc` (Go 1.24+ tool tracking) and required `otelc` package dependencies.
   - `go.mod` in Git will **NOT** contain hardcoded `replace` directives pointing to local uncommitted `./.otelc-build/` directories.
   - Local `.otelc-build/` and generated `cmd/*/otelc.runtime.go` files are generated on demand via `go generate` during instrumented build steps.

2. **Toolchain Compatibility**:
   - Avoid invalid `GOFLAGS` string quoting inside `Taskfile.yaml` and `.goreleaser.yml`.
   - Use `go tool otelc go build` or explicit `-toolexec` binary flags in build commands.

3. **Reproducible Tool Versioning**:
   - Pin `otelc` using Go 1.24+ `tool` directive in `go.mod` rather than `go install ...@latest`.

---

## 3. Detailed Component Plan

### 3.1 Pinned Tool & Instrumentation Dependencies (`otel.instrumentation.go` & `go.mod`)

Create `otel.instrumentation.go` at repository root:
```go
//go:build tools

//go:generate go tool otelc pin --generate
package tools

import (
	_ "go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel"
	_ "go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/init"
	_ "go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/sdk/trace"
	_ "go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/trace"
	_ "go.opentelemetry.io/otelc/instrumentation/google.golang.org/grpc/client"
	_ "go.opentelemetry.io/otelc/instrumentation/google.golang.org/grpc/server"
	_ "go.opentelemetry.io/otelc/instrumentation/log"
	_ "go.opentelemetry.io/otelc/instrumentation/log/slog"
	_ "go.opentelemetry.io/otelc/instrumentation/net/http/client"
	_ "go.opentelemetry.io/otelc/instrumentation/net/http/server"
	_ "go.opentelemetry.io/otelc/instrumentation/runtime"
)
```

Add `tool go.opentelemetry.io/otelc/tool/cmd/otelc` to `go.mod`.

### 3.2 Taskfile.yaml Targets

Add explicit tasks:
- `otelc:prep`: Runs `go generate ./otel.instrumentation.go`.
- `otelc:clean`: Restores `go.mod` / `go.sum` and cleans up temporary `.otelc-build/` and `cmd/*/otelc.runtime.go` files if working in dev mode.
- Update `build` / `build:otelc` to execute `go tool otelc go build` or run `otelc:prep` before compilation.

### 3.3 Dockerfile Stage

Update `Dockerfile`:
```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.26 AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM deps AS build
ARG BEAT
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
COPY . .
RUN go generate ./otel.instrumentation.go
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go tool otelc go build -ldflags "-s -w \
    -X main.version=${VERSION} \
    -X main.commit=${COMMIT} \
    -X main.date=${DATE}" -o /out/${BEAT} ./cmd/${BEAT}

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/ianeff/thump"
ARG BEAT
COPY --from=build /out/${BEAT} /usr/local/bin/beat
ENTRYPOINT ["/usr/local/bin/beat"]
```

### 3.4 .goreleaser.yml Configuration

Add `before.hooks`:
```yaml
before:
  hooks:
    - go mod tidy
    - go generate ./otel.instrumentation.go
```
Configure `builds[*]` to use `tool: go` with appropriate flags or `toolexec`.

---

## 4. Verification & Testing

1. `task ci` / `go test ./...` on fresh checkout: passes cleanly without `.otelc-build/` present.
2. `gopls` and `golangci-lint`: run clean without module resolution errors.
3. `task build` / Docker build: successfully generates instrumentation hooks and produces functional binaries with compile-time tracing.
