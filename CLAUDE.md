# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What clank is

A Go **decision engine**: it turns one `Signal` (a detected reliability event) into a
recorded, deduplicated, evidence-backed **Proposal**. It is the **Reason → gate → Propose**
slice of a resilience system — it reasons over read-only telemetry, decides whether a
remediation is warranted, and emits a structured proposal. It does **not** detect, does
**not** execute against infrastructure, and does **not** deliver tickets/PRs itself.

In the four-plane agentic-reliability architecture, clank is the **Reasoning Plane**: it
**selects** (generates hypotheses, ranks candidate actions) — it does **not permit**
(authority/policy is the Governance Plane's job, which clank does **not** build) and it does
**not detect** (that's the Signal Plane — the sibling project `rattle`, whose `Signal` is
clank's input). "Selects vs. permits" is the boundary the whole design rests on; see § The
clank ⟷ rattle boundary below.

Long-running service. Module `github.com/ianeff/clank`, Go 1.26. Structured `slog`
logging, context-driven graceful shutdown.

> The repo is currently an early sketch (`internal/clank/clank.go` is a partial loop;
> `clank_test.go` references a `Hello()` that doesn't exist, so the package does not yet
> build). **The authoritative design is in the vault — build from there.**

## How we work together (read this)

This is a **learning project** as much as a build: Ian is using it to learn Go, and will
lean on Claude heavily as a pairing partner to figure out *how* to implement the code. So:

- **Teach, don't just type.** Explain the *why* — the Go idiom at play, what a test is
  pinning, what a failure proves — not only the final code. Surface the reasoning behind the
  move so the lesson sticks.
- **Hold TDD loosely.** The implementation guide is written red→green, and that's a great
  spine, but we are deliberately **not dogmatic** about it. Ian has ADHD and follows energy
  and curiosity — sometimes that means writing a test first, sometimes spiking the code to
  see it work, sometimes jumping waves or chasing a tangent. Follow where the interest goes;
  bring it back to tests when it's useful, not as a ritual.
- **Go at the user's pace, not ahead of it.** Don't race off and implement multiple waves
  unprompted. Do a chunk, talk about it, leave room for Ian to drive.
- **Name the concept** (table tests, error wrapping, interface seams, `synctest`, escape
  analysis) so the lesson generalizes beyond this repo.
- Building something awesome is the goal; Ian getting fluent in Go is the point — and it's a
  learning process for both of us. Optimize for that, not for process purity. 🤖

## Source of truth: the Obsidian vault

The canonical scope, architecture, and build plan live in the vault at `~/Documents/vault`,
under `~/Documents/vault/Projects/clank/`:

- `clank-readme.md` — anchor / one-page overview. Read first.
- `clank-implementation-guide.md` — the **test-first (red→green) build walkthrough**. Every
  type is defined as real Go; each behaviour has its test code and the production code it
  forces into existence. This is the build; follow it top-to-bottom.
- `clank-running-notes.md` — investigation journal; where open decisions get pinned.
- `clank-todo.md` — the live checklist (Wave 0→3, claim by claim).

A canonical scope doc is destined for this repo at `docs/decision-engine-scope.md` (not yet
written). Read the vault docs live — do not mirror them into the repo. Note: vault docs
sometimes use the stale module path `github.com/ifurst/clank`; the real path is
`github.com/ianeff/clank` (see `go.mod`).

## Architecture (the one-sentence shape)

One `Signal` comes in → a bounded loop reasons over read-only telemetry → a gate checks the
result → a `Proposal` comes out, recorded, deduped, evidence-backed. Nothing touches
infrastructure.

`Engine.Propose(ctx, Signal) (Outcome, error)` runs the loop: seed messages → call
`Model.Complete` → checkpoint each turn (`Store`) → the model either calls read-only
telemetry `Tool`s (digests appended, loop again) or calls `propose`/`insufficient`. On
`propose`, a `Decision` is judged by the `Gate` (evidence? duplicate?), recorded to the
`ProposalLog`, and delivered via `ProposalSink` only if admitted.

The vocabulary is small and fixed — do not invent new nouns. Data: `Signal`, `EvidenceRef`,
`Decision`, `Hypothesis`, `Status` (closed enum), `Outcome`. Seams (interfaces): `Gate` (impl
`ReadinessGate`), `Store`, `ProposalLog`, `Model`, `Tool`, `ProposalSink`. See
`clank-implementation-guide.md` § THE CAST for the exact definitions.

`Decision` **is the Candidate Action** boundary object — the audit record of the reasoning
chain handed across the Reasoning→Governance seam. It carries `Hypothesis` + `Alternatives`
(competing hypotheses with weights), `Evidence`, `Confidence` (clank's *candidate-action*
confidence), `RequestedAuthorityLevel` (a **request**, never a verdict), and the
`Fingerprint` **copied** from the inbound `Signal`.

### The clank ⟷ rattle boundary (do not blur)

clank is the **Reasoning Plane**; `rattle` is the **Signal Plane**. The safety of the whole
design rests on this seam holding. Three rules:

1. **The `Signal` is rattle's, not ours.** clank consumes it read-only and **trusts it** — it
   never recomputes the fingerprint (`kind:object`, assigned by rattle), never re-judges
   freshness/significance. The `Signal` definition in the vault guide is reproduced *for
   reference*; rattle owns it.
2. **Two confidence numbers, never one field.** *Signal-level* confidence (input
   trustworthiness) lives on the `Signal` and is **rattle's** — clank reads it, never sets it.
   *Candidate-action* confidence (hypothesis certainty) lives on the `Decision` and is
   **clank's** — computed from evidence. Don't conflate them.
3. **clank selects; it does not permit.** The gate's `Verdict{Admit}` means "this proposal is
   worth **emitting**," NOT "this action is authorized." The gate has **zero policy** in it —
   no criticality tier, no error-budget check, no confidence threshold. Those belong to a
   Governance Plane clank does **not** build; putting any of them in clank is the seam that
   rots first. clank emits `RequestedAuthorityLevel` and stops.

### The six invariants (these ARE the spec)

1. **Digests only** — raw payloads never enter state/messages. `EvidenceRef` has no raw
   field by design; a `Tool` literally cannot return raw data.
2. **Bounded loop** — the reason loop stops at `MaxSteps` (→ `budget_exhausted`).
3. **Evidence required** — a `Decision` with no evidence is rejected (`insufficient_evidence`).
4. **Dedup** — an open proposal for the same fingerprint suppresses a new one
   (`suppressed_duplicate`); suppressed means recorded but NOT delivered.
5. **Checkpoint-per-turn** — a `Store.Checkpoint` error halts the run (re-running is safe;
   proposing mutates no infra).
6. **Read-only** — the model is offered only read-only `Tool`s plus the `propose`/
   `insufficient` control specs.

### Deliberately NOT built (do not build or test these)

A **Governance plane / any authority decision** (clank emits `RequestedAuthorityLevel` and
stops — no criticality, error-budget, change-window, or confidence-threshold check anywhere);
**signal validity / significance / fingerprinting** (rattle's job — clank trusts the inbound
`Signal` and copies its fingerprint); **confidence *computation* and *gating*** (the
`Decision.Confidence` field exists so the two-confidence boundary holds, but rich computation
is a placeholder and any *threshold* on it is a Governance leak — the gate stays evidence +
dedup only; signal-level confidence is rattle's, never computed here); `ExecuteActuator` / any
infra-mutating path; real OTel/Detect (rattle) wiring (Signal source is stubbed);
Postgres/Temporal backends (in-memory only); token streaming / multi-provider SDK (one fake
`Model`).

## Trajectory

Two phases. Phase 1 is the whole of the current build; phase 2 is gated on it.

- **Phase 1 — the binary (now).** The test-first Decision Engine: `Engine.Propose(ctx,
  Signal) → Outcome`, all six invariants green, `make ci` clean. **This is the only thing in
  scope until it works.**
- **Phase 2 — the operator (after the binary works).** Wrap the working engine as a
  Kubernetes operator (controller-runtime / kubebuilder): a reconciler watches for `Signal`s
  (as CRs, or off rattle) and calls `Engine.Propose`; the Candidate Action surfaces as a
  `Proposal` CR / status / event.

**Phase 2 does not change phase 1.** The operator is a **delivery/trigger surface** — a new
*caller* of `Engine.Propose` plus a `ProposalSink` impl. The core types, the gate, and the
six invariants are untouched; the `Signal` just arrives via a watch instead of a stub. Do not
pre-build operator scaffolding while phase 1 is unfinished.

## Working with the tests (a spine, not a cage)

The implementation guide lays out a test list (Wave 0→3) written red→green, and it's a good
map of what to build and in what order. Use it as a guide, not a mandate — see "Hold TDD
loosely" above. When we do write tests, these conventions keep them sharp:

- Name tests as falsifiable claims (Action·Condition·Expectation), e.g.
  `TestGate_RejectsDecisionWithNoEvidence` — `gotestdox ./...` then reads the suite back as a
  spec.
- Failure messages name the user-visible failure plus `cmp.Diff(want, got)` — not
  `want %v got %v`.
- Tests live in package `clank_test` (external), so they exercise the API as a real caller would.
- When a failing test comes first, confirming the *specific* red you predicted (not a panic
  or compile error) is what proves the test has teeth — worth doing when it matters, skippable
  when you're spiking.

## Definition of done

- `make ci` is green: fmt-check → vet → lint → test (`-race`) → build. Run checks/tests
  incrementally during edits.
- `make vulncheck` is clean — a separate security gate (govulncheck over deps), not part of
  `make ci`.

## Commands

- `make run` — run the service (`go run ./cmd/clank`).
- `make build` — build to `bin/clank` (injects version/commit/date ldflags); `./bin/clank --version`.
- `make ci` — full local CI: fmt-check → vet → lint → test → build.
- `make test` / `make race` — tests, with `-race`.
- `make coverage` — coverage profile + total.
- `make vulncheck` — govulncheck over deps.
- Single test: `go test ./internal/clank -run TestGate -v` (add `-race` for concurrency).
- `gotestdox ./...` — read test names back as a spec sentence list.

## Go house rules

- Errors: wrap with `%w`, compare with `errors.Is` / `errors.As`, combine with `errors.Join`. Package-level `var ErrFoo = errors.New(...)` for sentinels.
- Never return a typed-nil pointer as an `error` — return literal `nil`.
- Accept interfaces, return structs. Interfaces are consumer-defined, not shipped with the implementation.
- `context.Context` is the first parameter, never a struct field. Thread it through; no `context.Background()` deep in call chains.
- Run `go test -race` for concurrency. Use `testing/synctest` (`synctest.Test`) for deterministic time/concurrency tests.
- Benchmark with `testing.B` and `benchstat` before/after. Check escape analysis via `go build -gcflags=-m`.
- Use stdlib: `any` (not `interface{}`), builtins (`min`/`max`/`clear`), `log/slog`, `slices`/`maps` over hand-rolled loops.
- Don't guess signatures or find-replace blindly — use `go doc` or gopls/LSP tools (`go_rename_symbol`, etc.).

## Service shape

- Operational output goes through the default `slog` JSON handler — no `fmt.Println`.
- Shutdown is driven by `signal.NotifyContext`; new long-running work selects on `ctx.Done()` and exits cleanly.
