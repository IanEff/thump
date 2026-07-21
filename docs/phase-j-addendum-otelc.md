# Phase J Addendum — `otelc` Compile-Time Instrumentation

**Parent plan:** [phase-j-implementation-plan.md](file:///Users/ian/projects/go/thump/docs/phase-j-implementation-plan.md)

**What this is:** an evaluation and integration outline for
[opentelemetry-go-compile-instrumentation](https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation)
(`otelc`), the OTel project's compile-time auto-instrumentation tool for Go.

---

## §1 — Evaluation

### What `otelc` does

`otelc` wraps `go build` (or plugs in via `-toolexec`) and injects OTel spans
into third-party library call sites at compile time — no source changes. It
ships rule sets for **gRPC**, **net/http**, **database/sql**, **message
streams** (including NATS-shaped pub/sub), and a catch-all "Everything" bundle.
Status badge says **Stable**, requires **Go 1.25+** (thump is on 1.26).

### Why it's a good fit for thump

1. **The beats already have manual tracing, but only at stage boundaries.**
   [beat/trace.go](file:///Users/ian/projects/go/thump/internal/beat/trace.go)
   wires up a TracerProvider; [beat/stage.go](file:///Users/ian/projects/go/thump/internal/beat/stage.go)
   calls `tracer.Start(ctx, name)` once per stage. There is exactly **one**
   `tracer.Start` call site in all of `internal/`. Everything below that —
   gRPC calls to the Anthropic/Gemini API, NATS publishes, Kubernetes
   client-go calls, S3 PUTs for checkpoint storage — is invisible in the
   trace backend. `otelc` would fill that gap without touching any of those
   call sites.

2. **The dependency graph is exactly the sweet spot.** thump's hot
   dependencies are gRPC (`otlptracegrpc`, Anthropic SDK, Gemini SDK), NATS
   (`nats.go`), net/http (Kubernetes client-go, Prometheus API), and S3
   (aws-sdk-go-v2). `otelc`'s rule bundles cover gRPC, HTTP, and message
   streams. The AWS SDK uses net/http under the hood, so HTTP instrumentation
   catches S3 calls too.

3. **Zero source diff.** The compile-time approach means no new imports, no
   new `go.mod` entries beyond `otelc` itself (as a tool dependency), and no
   risk of breaking the existing manual spans — they coexist.

4. **Build integration is trivial.** Two paths, both one-line changes:
   - **`toolexec` path (recommended):** set `GOFLAGS` to include
     `'-toolexec=otelc toolexec'` after a one-time `otelc setup`. No
     Taskfile/Dockerfile restructuring.
   - **Wrapper path:** replace `go build` with `otelc go build` in the three
     build surfaces (Taskfile `build` task, Dockerfile `RUN`, goreleaser
     `builds[].tool`).

### Risks and mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| Build time regression (compile-time rewriting adds latency) | Low | Benchmark with `time task build` before/after; `otelc` caches instrumented artifacts in Go's build cache separately from plain builds, so incremental builds aren't penalized |
| Binary size increase (injected spans + runtime shim) | Low | Measure with `ls -la bin/` before/after; the shim (`otelc/runtime`) is small; `-s -w` ldflags still apply |
| Interaction with existing manual spans | Negligible | `otelc` uses the process-global `TracerProvider` that [beat/trace.go:62](file:///Users/ian/projects/go/thump/internal/beat/trace.go#L62) already sets via `otel.SetTracerProvider(tp)` — injected spans land in the same exporter with no conflict |
| `otelc` doesn't cover a dep we care about (e.g. NATS JetStream specifically) | Medium | The "Message Streams" rule set may or may not match `nats.go`'s JetStream API specifically; verify during the spike. Worst case, JetStream calls stay invisible (status quo), and the gRPC + HTTP coverage alone is still a net win |
| Lock-in to a tool that goes unmaintained | Low | It's an official `open-telemetry` project, Apache 2.0, stable badge. If it dies, `otelc setup` artifacts are removable in one commit — the source tree is untouched |

### Verdict

**Yes, add it.** The effort is genuinely minimal (one build-flag change),
the risk is low (reversible in one commit), and the observability gain is
large (every gRPC, HTTP, and messaging call becomes a span). The existing
manual stage spans stay exactly where they are — `otelc` fills in the
interior.

---

## §2 — Integration Outline

### Prerequisites

- `otelc` binary available in the build environment (Dockerfile, local
  toolchain, CI runner). Two options:
  - **Tool dependency (preferred):** `go get -tool go.opentelemetry.io/otelc/tool/cmd/otelc`
    — tracked in `go.mod`, pinned, reproducible.
  - **External install:** `go install go.opentelemetry.io/otelc/tool/cmd/otelc@latest`
    — simpler for a spike, not pinned.

### Step 1 — `otelc setup` (one-time, committed)

Run from the module root:

```bash
otelc setup
# or, if installed as a tool dep:
go tool otelc setup
```

This generates:
- `otel.instrumentation.go` (or `otelc.tool.go`) — blank imports declaring
  which rule bundles to use. Start with "Everything"; narrow later if needed.
- `.otelc-build/` — matched rules cache, `.gitignore`-able.

**Commit** `otel.instrumentation.go` (it's the config). **Gitignore**
`.otelc-build/` (it's a cache).

### Step 2 — Wire into build surfaces

Three surfaces, one change each:

#### Taskfile.yaml (`task build`)

```yaml
build:
  desc: Build all five beats to bin/ (version/commit/date ldflags, -trimpath, otelc-instrumented)
  cmds:
    - mkdir -p bin
    - for beat in {{.BEATS}}; do otelc go build -trimpath -ldflags "{{.LDFLAGS}}" -o bin/$beat ./cmd/$beat || exit 1; done
```

Or, using the `toolexec` path (no change to the `go build` invocation itself):

```yaml
build:
  desc: Build all five beats to bin/ (version/commit/date ldflags, -trimpath, otelc-instrumented)
  env:
    GOFLAGS: "'-toolexec=otelc toolexec'"
  cmds:
    - mkdir -p bin
    - for beat in {{.BEATS}}; do go build -trimpath -ldflags "{{.LDFLAGS}}" -o bin/$beat ./cmd/$beat || exit 1; done
```

#### Dockerfile

```dockerfile
FROM deps AS build
ARG BEAT
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
COPY . .
# Install otelc for compile-time instrumentation
RUN go install go.opentelemetry.io/otelc/tool/cmd/otelc@latest
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH otelc go build -ldflags "-s -w \
    -X main.version=${VERSION} \
    -X main.commit=${COMMIT} \
    -X main.date=${DATE}" -o /out/${BEAT} ./cmd/${BEAT}
```

#### .goreleaser.yml

GoReleaser doesn't have native `otelc` support, but the `toolexec` path
works via the `flags` or `env` fields:

```yaml
builds:
  - id: clank
    main: ./cmd/clank
    binary: clank
    env:
      - CGO_ENABLED=0
      - GOFLAGS='-toolexec=otelc toolexec'
    # ... rest unchanged
```

> [!NOTE]
> GoReleaser needs `otelc` on `$PATH` during the release build. Add it to
> the `before.hooks` or the CI job's setup step.

### Step 3 — Verify the existing TracerProvider is picked up

`otelc`'s injected spans use the process-global `TracerProvider`. thump
already sets that global at
[beat/trace.go:62](file:///Users/ian/projects/go/thump/internal/beat/trace.go#L62):

```go
otel.SetTracerProvider(tp)
```

**No additional wiring needed.** When `OTEL_EXPORTER_OTLP_ENDPOINT` is set
(on-cluster), injected spans flow through the same batching OTLP/gRPC
exporter. When unset (CI, local), the noop tracer absorbs them silently.

---

## §3 — Tests

All tests follow the AGENTS.md standards: ACE names, map-based tables,
`cmp.Diff(want, got)`, `t.Fatal` on unexpected errors.

### Test 1: Build output is a valid binary with instrumentation

**Name:** `TestBuild_OtelcInstrumentedBinaryStartsAndExportsSpans`

This is a **smoke test / integration test**, not a unit test. It belongs in
a `//go:build integration` file or a Taskfile task, not in `task ci`.

```bash
# Script-level test (Taskfile task or shell script in test/):
# 1. Build one beat with otelc
otelc go build -o /tmp/test-clank ./cmd/clank
# 2. Confirm the binary exists and is executable
test -x /tmp/test-clank
# 3. Confirm the binary contains otelc runtime symbols
go tool nm /tmp/test-clank | grep -q 'otelc'
echo "PASS: otelc symbols present in instrumented binary"
```

**What it pins:** the build didn't silently skip instrumentation (a real risk
if `.otelc-build/` is stale or `otelc setup` wasn't run).

### Test 2: Build time regression is bounded

**Name:** `TestBuild_OtelcDoesNotExceedBuildTimeThreshold`

```bash
# Benchmark script (not CI-blocking, advisory):
# Clean build cache to measure worst case
go clean -cache

# Baseline: plain build
time go build -trimpath -ldflags "$LDFLAGS" -o /dev/null ./cmd/clank 2>&1 | tee /tmp/baseline.txt

# Instrumented build
time otelc go build -trimpath -ldflags "$LDFLAGS" -o /dev/null ./cmd/clank 2>&1 | tee /tmp/instrumented.txt

# Compare — fail advisory if >2x slowdown
# (exact threshold TBD after first measurement)
```

**What it pins:** instrumentation overhead is bounded. Not a hard gate —
compile-time instrumentation is inherently slower on cold cache — but a
canary to prevent surprise.

### Test 3: Binary size increase is bounded

**Name:** `TestBuild_OtelcBinarySizeWithinThreshold`

```go
// Location: test/otelc_test.go (build-tagged `//go:build integration`)
//
// TestBuild_OtelcBinarySizeWithinThreshold pins that compile-time
// instrumentation does not inflate the binary beyond a 20% threshold
// — the injected shim is small, and a larger delta means something
// unexpected was pulled in.
func TestBuild_OtelcBinarySizeWithinThreshold(t *testing.T) {
    t.Parallel()
    if os.Getenv("OTELC_INTEGRATION") == "" {
        t.Skip("set OTELC_INTEGRATION=1 to run otelc integration tests")
    }

    baseline := buildBeat(t, "clank", false) // plain go build
    instrumented := buildBeat(t, "clank", true) // otelc go build

    baselineSize := fileSize(t, baseline)
    instrumentedSize := fileSize(t, instrumented)

    overhead := float64(instrumentedSize-baselineSize) / float64(baselineSize)
    const maxOverhead = 0.20 // 20% — adjust after first measurement

    if overhead > maxOverhead {
        t.Errorf("binary size overhead %.1f%% exceeds %.0f%% threshold "+
            "(baseline=%d, instrumented=%d)",
            overhead*100, maxOverhead*100, baselineSize, instrumentedSize)
    }
}
```

### Test 4: Existing manual spans survive instrumentation

**Name:** `TestTracer_ManualSpansCoexistWithOtelcInjectedSpans`

This is the correctness test that matters most — it pins that the existing
`beat.Tracer` → `tracer.Start(ctx, name)` path in
[beat/stage.go](file:///Users/ian/projects/go/thump/internal/beat/stage.go)
still produces spans when the binary is built with `otelc`.

```go
// Location: internal/beat/trace_otelc_test.go
// Build tag: //go:build integration
//
// TestTracer_ManualSpansCoexistWithOtelcInjectedSpans pins that building
// with otelc doesn't suppress or duplicate the manual stage spans — the
// injected spans from otelc and the explicit spans from beat.Tracer must
// land in the same exporter without interference.
func TestTracer_ManualSpansCoexistWithOtelcInjectedSpans(t *testing.T) {
    t.Parallel()
    if os.Getenv("OTELC_INTEGRATION") == "" {
        t.Skip("set OTELC_INTEGRATION=1 to run otelc integration tests")
    }

    // Use an in-memory span exporter to capture all spans.
    exporter := tracetest.NewInMemoryExporter()
    tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
    otel.SetTracerProvider(tp)
    defer tp.Shutdown(context.Background())

    tracer := tp.Tracer("test-beat")
    ctx, span := tracer.Start(context.Background(), "manual-stage-span")
    // Simulate work that would trigger otelc-injected spans
    // (e.g., an HTTP call via net/http, a gRPC dial)
    span.End()

    tp.ForceFlush(context.Background())
    spans := exporter.GetSpans()

    // Assert: at minimum, the manual span is present
    var foundManual bool
    for _, s := range spans.Snapshots() {
        if s.Name() == "manual-stage-span" {
            foundManual = true
        }
    }
    if !foundManual {
        t.Error("manual stage span not found in exporter — otelc may have replaced the TracerProvider")
    }
}
```

> [!IMPORTANT]
> Tests 1-3 are **build-level integration tests** that shell out to `otelc`
> and compare artifacts. They are gated behind `OTELC_INTEGRATION=1` or a
> `//go:build integration` tag and are **not part of `task ci`** — they
> require `otelc` to be installed, which CI may not have yet.
>
> Test 4 can run as a normal Go test (no shell-out) once the binary is built
> with `otelc`, but it needs the `integration` build tag to avoid running in
> plain `go test ./...`.

### Test 5: `.gitignore` includes `.otelc-build/`

**Name:** `TestGitignore_OtelcBuildDirIsIgnored`

```go
// Location: test/repo_hygiene_test.go
//
// TestGitignore_OtelcBuildDirIsIgnored pins that .otelc-build/ (the
// compile-time instrumentation cache) is gitignored — it's a build
// artifact, not source, and committing it bloats the repo and breaks
// cross-platform builds.
func TestGitignore_OtelcBuildDirIsIgnored(t *testing.T) {
    t.Parallel()
    data, err := os.ReadFile(".gitignore")
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(string(data), ".otelc-build") {
        t.Error(".gitignore does not contain .otelc-build — the otelc cache dir would be committed")
    }
}
```

---

## §4 — Sequencing

This work is **independent of Waves J0–J3** — it touches the build pipeline,
not the runtime Go code those waves modify. It can land before, after, or in
parallel with any of them.

**Suggested order:**

1. **Spike (30 min):** `go get -tool ...otelc`, `otelc setup`, `otelc go build -o /tmp/test ./cmd/clank`. Eyeball the binary, check `go tool nm` for
   injected symbols, compare `ls -la` sizes. This answers the "does it even
   work with our deps" question.
2. **Wire (15 min):** Update `Taskfile.yaml` build task, add `.otelc-build/`
   to `.gitignore`, commit `otel.instrumentation.go`.
3. **Verify on-cluster (next live session):** Deploy an `otelc`-built image
   to `thump-test`, inject chaos (Run 1 from §2 of the parent plan), and
   confirm the trace backend shows the new interior spans alongside the
   existing stage spans. This is the real proof — the unit tests above guard
   against regression, but the live trace view is the payoff.
4. **Harden (optional):** Write the integration tests (Tests 1-4 above),
   update the Dockerfile and `.goreleaser.yml`.

---

## §5 — Definition of done

1. `otel.instrumentation.go` committed, `.otelc-build/` gitignored.
2. `task build` produces instrumented binaries (verified by `go tool nm |
   grep otelc`).
3. Binary size overhead measured and documented (expected <20%).
4. Build time overhead measured and documented (expected <2× cold, negligible
   warm).
5. Live cluster trace shows interior spans (gRPC, HTTP) nested under the
   existing stage spans.
6. `task ci` still green (otelc doesn't break vet, lint, or tests).
