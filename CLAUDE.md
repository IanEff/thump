# CLAUDE.md

Guidance for Claude Code when working in this repository.

## What is thump

A five-beat agentic reliability engine: **rattle** (detect) → **clank** (reason with
LLM) → **hiss** (govern) → **thump** (act) → **click** (learn). All beats live in this
monorepo under `internal/{rattle,clank,hiss,thump}`; click has no package of its own —
it's `internal/clank`'s `click.go` (`Click.Absorb`, `ReturnEdge`) and `metrics.go`'s
`Recorder`, running live in broker mode. Modularization to NATS JetStream is a future
phase.

## Skills

Invoke these before the situation they cover — they carry the actual how-to, this file
only points to them:

- **go-standards** — before writing or editing any `.go` file (code, comments, tests).
  Loads `AGENTS.md`'s house rules, which are the real spec.
- **go-navigation** — before searching or reading unfamiliar Go code. Use the gopls MCP
  tools, not grep-first.
- **chaos-testing** — before `tilt up`, a `chaos/*.sh` script, or reading a live run's
  results. Covers rig layout and event-driven waiting.
- **debug-transcript** — when a clank model decision looks wrong. Read the actual S3
  transcript, not a reconstruction from logs.

## Key design principles

- **No infra; the LLM is bounded.** The model proposes only catalogued actions; the
  loop has a `MaxSteps` limit.
- **Digests only, never raw.** Tools return `EvidenceRef`, never raw payloads; raw data
  cannot enter the conversation.
- **The catalog bounds; it does not reason.** The LLM generates hypotheses and picks
  from the catalog; the autonomy boundary is enforced behaviourally.
- **The set is the audit unit.** The whole ranked `ProposalSet` is emitted and
  recorded, not just the top choice.
- **Gate is conjunction of minimums.** `budget ∧ dedup ∧ evidence` — one failing
  dimension vetoes.
- **Five belief-formation defences.** ≥2-source floor, freshness-decay, negative-signal
  check, partial-convergence tracking, forced-live-citation.

**Deferred (intentionally not built):** governance/authority checks in clank (that's
hiss), risk shaper, signal validity checks (rattle's), learning-loop writes,
parallel-decision, real source backends.

The real `Model` client shipped: `AnthropicModel` (`internal/clank/model_anthropic.go`)
is what `clank.go` wires in, hitting Claude Haiku over the Messages API. `GeminiModel`
(`model_gemini.go`) exists as a second `Model` implementation but nothing selects it
yet.

## Definition of done

- `task ci` green: fmt-check → vet → lint → vulncheck → chart-lint → race → build.
  Lint is GitHub's gate — red lint ≠ passing tests.
- Five belief-formation defences tested green (not tested = decorative).
- Autonomy boundary behavioural — a test proves the LLM can't propose off-catalog
  actions.
- Loop invariants green — bounded, checkpoint-or-halt, digests-only, read-only.
- No deferred things got built.
- Every `//nolint:gosec` carries a reason comment naming the gosec rule ID — see
  `AGENTS.md`.

## Commands

[go-task](https://taskfile.dev) (`Taskfile.yaml`); see `task --list-all` for the full
set.

- `task run:{clank,rattle,hiss,thump}` — run one beat.
- `task ci` — full CI chain.
- `task test` / `task race` — tests ± race detector.
- `task coverage` — coverage report.
- Single test: `go test ./internal/clank -run TestGate -v`.

## Go standards

Read `AGENTS.md` for Go idioms, comment style, and test conventions (the go-standards
skill enforces this — see Skills above). This file doesn't duplicate them.

## Service shape

- Operational output: default `slog` JSON handler, no `fmt.Println`.
- Shutdown: `signal.NotifyContext`; new work selects `ctx.Done()` and exits cleanly.
- Two observability layers, never fused: the audit trail (SAO, ProposalSet, evidence
  refs, rationales) answers "why?"; telemetry (slog + RED + spans at seams) answers
  "how fast?".
