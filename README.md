# thump

> A Go **agentic reliability engine** implementing the five-beat DRAL architecture
> (Detect, Reason, Act, Learn — plus Govern). It turns a raw infrastructure signal 
> into a confident, reasoned proposal, and ultimately, an executed mitigation.

`thump` is the umbrella project for a monorepo containing five cooperating but distinct "beats" (planes):
1. **rattle** (Signal/Detect): detects reliability divergences, emits a fingerprinted `SignalDetection`.
2. **clank** (Reason): the LLM reason loop. It assembles a snapshot of the incident (the SAO), investigates with read-only tools, generates hypotheses, and proposes candidate actions with dynamic confidence.
3. **hiss** (Govern): evaluates clank's `ProposalSet` against policy to emit an allow/deny/escalate `Decision`.
4. **thump** (Act): executes the approved mitigation.
5. **click** (Learn): closes the loop (future).

- **Module:** `github.com/ianeff/thump`
- **Go:** 1.26
- **Shape:** five beats in one monorepo (for now); structured `slog` logging; context-driven graceful shutdown.

> **Phase 1 is over, the DRAL vision is reality.** Originally started as just `clank` (the reasoning plane), the repository was renamed to `thump` to reflect the full multi-beat system. `rattle`, `clank`, and `hiss` are all actively built and green in this repository.
---

## Table of contents

- [The five-beat architecture](#the-five-beat-architecture)
- [The boundaries (do not blur)](#the-boundaries-do-not-blur)
- [The clank reason loop](#the-clank-reason-loop)
- [clank module seams](#clank-module-seams-one-file-per-concern)
- [Loop invariants](#loop-invariants-these-are-the-spec)
- [The five belief-formation defences](#the-five-belief-formation-defences-why-clank-exists)
- [Boundary objects & vocabulary](#boundary-objects--vocabulary)
- [Repository layout & current state](#repository-layout--current-state)
- [Building & testing](#building--testing)
- [How we build: test-first, wave by wave](#how-we-build-test-first-wave-by-wave)
- [Trajectory: Phase 1 is done, Phase 2 is here](#trajectory-phase-1-is-done-phase-2-is-here)
- [Contributing](#contributing)
- [Source of truth](#source-of-truth)

---

## The five-beat architecture

This project implements an *agentic reliability* architecture (from the book *Agentic
Reliability Engineering*). The design rests on a strict separation of five beats (planes):

| Plane | Project (internal/*) | Job | Verb |
|---|---|---|---|
| **Signal** | `rattle` | detect reliability divergences, emit a fingerprinted `SignalDetection` | *detects* |
| **Reasoning** | `clank` | reason over evidence, generate hypotheses, propose + rank candidate actions | *selects* |
| **Governance** | `hiss` | evaluate a `ProposalSet` against policy to emit an allow/deny/escalate `Decision` | *permits* |
| **Execution** | `thump` | act against infrastructure, observe outcomes | *acts* |
| **Learning** | `click` | update the case base and adjust confidence based on outcomes | *learns* |

"Selects vs. permits" is the boundary the core design rests on. The Reasoning plane (`clank`) generates hypotheses and recommends actions, but the Governance plane (`hiss`) holds the policy for whether those actions are allowed.

Three hard lines clank never crosses:

1. **It does not detect.** `SignalDetection` is rattle's; clank trusts it.
2. **It does not execute.** It emits proposals; nothing touches infrastructure.
3. **It does not authorize.** Each proposal carries a *requested* governance band; `hiss` converts it to allow/deny.
---

## The boundaries (do not blur)

The boundaries between planes are strict compiler-enforced seams. The safety of the whole design rests on these holding.

### The rattle ⟷ clank boundary
clank consumes rattle's `SignalDetection` read-only. Three rules:

1. **The `SignalDetection` is rattle's, not ours.** clank consumes it read-only and
   **trusts it** — it never recomputes the fingerprint (rattle's dedup key), never re-judges
   signal trustworthiness or significance. clank imports rattle's type via the `internal/signal` package.
2. **Two confidence numbers, never one field.**
   - *Signal-strength* confidence lives on `SignalDetection.Divergence.Confidence` and is **rattle's** — clank reads it, never sets it.
   - *Hypothesis* confidence lives per `Candidate` and is **clank's**, computed by the reason loop.
3. **Two-axis impact, never collapsed.** rattle hands clank **severity** (how bad) and **blast radius** (how broadly exposed) as independent axes.

### The clank ⟷ hiss boundary
1. **clank selects; it does not permit.** The readiness gate in clank decides whether a `ProposalSet` is worth **emitting**, NOT whether an action is authorized. It holds **zero policy**.
2. **hiss governs.** Each `Candidate` carries a `GovernanceLevel` band (a *request*); `hiss` converts the band to an approved, escalated, or rejected `Decision`.
---

## The clank reason loop

`Engine.Propose(ctx, SignalDetection) (ProposalSet, error)` runs the loop:

```
SignalDetection (rattle, read-only)
  ① INTAKE       assemble the SAO (Option B — clank does the reading): SignalSnapshot +
  │               TopologySnapshot + ChangeSnapshot, versioned
  ② REASON LOOP  seed []Message from the SAO, then bounded loop (≤ MaxSteps):
  │               Model.Complete(msgs, tools) → checkpoint each turn (Store)
  │                 ├─ telemetry tool  → run read-only, append the DIGEST (never raw), loop
  │                 ├─ case-base tool  → retrieve similar past incidents (Learn edge), loop
  │                 ├─ "propose"       → model emits hypotheses + candidate actions (drawn
  │                 │                     from the catalog) + per-hypothesis confidence → exit
  │                 └─ "insufficient" / no tool calls → no_action → exit
  ③ GROUND       belief-formation guardrails: ≥2-source floor · freshness-decay ·
  │               negative-signal checks
  ④ RANK         order candidates by effectiveness / risk / reversibility / time-to-effect,
  │               velocity-weighted off the signal's blast-radius (deterministic, auditable)
  ⑤ GATE         readiness = budget ∧ dedup ∧ evidence (conjunction of minimums, never an
  │               average). Pass → emit · fail → silence
  ⑥ EMIT         ranked ProposalSet, recorded to the ledger, delivered via ProposalSink only
                  if the gate passed
```

**Why a loop, not a pipeline.** The Reason beat is iterative: the model investigates (calls
telemetry tools, retrieves similar incidents), and *not acting is a valid outcome*
(`insufficient`). The loop is bounded (`MaxSteps`) and every turn is checkpointed (`Store`)
so a crashed run resumes. Ranking and the gate run **once** on the formed set, after the
loop exits. Intake reads sources, the loop calls the `Model` and tools, emit writes —
everything between (causal scorer, ranker, gate) is a pure, table-testable function.

**The plain-English version:** clank is a smart on-call assistant that investigates an alert
and writes up an incident proposal — *but has no hands*. It reads dashboards and logs; it
cannot touch production. Its entire output is a document: *"here's what I think is breaking,
here's my evidence, here are the 2–3 things you could do, ranked, and here's the one I'd
pick."* A human (or a later governance layer) decides whether to act.

---

## clank module seams (one file per concern)

Phase 1 is **one `internal/clank` package, one file per seam**. The file boundaries express
the module table; the discipline is the **must-not** column — that's where a clean design
rots first if you let a concern bleed across.

| Module (file) | Owns | In → Out | Must **not** |
|---|---|---|---|
| `intake` (`intake.go`, `sao.go`) | assemble + version the **SAO** | `SignalDetection` → `SAO` | reason or gate — only gather + freeze |
| `model` (`model.go`) | one method: complete a turn given messages + offered tools | `([]Message, []ToolSpec)` → `Completion` | hold state |
| `engine` (`engine.go`) | drive the bounded loop, dispatch tools, checkpoint, form the set | `SAO` → `[]Candidate` (+ hypotheses) | execute infra; exceed `MaxSteps` |
| `tools` (`tools.go`) | read-only telemetry + case-base retrieval; return **digests** | `args` → `EvidenceRef` | mutate; return raw payloads |
| `catalog` (`catalog.go`) | store `ActionContract`s = the **autonomy boundary** | `(FailureClass, tier, SAO)` → applicable `[]ActionContract` | reason or rank |
| `causal` (`causal.go`) | score change-event causality **+ enforce belief defences** | `ChangeSnapshot` → `[]CausalScore` (+ `Rationale`) | rank |
| `ranker` (`rank.go`) | order the model's candidates | `([]Candidate, velocity)` → ranked set + rationale | gate / decide emission |
| `gate` (`gate.go`) | readiness decision only | `(ProposalSet, openDupes, policy)` → `GateResult` | hold **any** policy/shaping/authority |
| `store` (`store.go`) | durable per-turn checkpoint so a run resumes | `Turn` → persisted | be the proposal ledger |
| `ledger` (`ledger.go`) | dedup query + record of emitted sets | `(fingerprint, since)` → open sets | judge |
| `sink` (`sink.go`) | render/deliver the `ProposalSet` | `ProposalSet` → out | mutate infra |
| `policy` (`policy.go`) | supply tunables read each reconcile | `GatePolicy` → thresholds/weights | be hardcoded |

Three seams deserve emphasis because the design (and the book) blur them:

- **The catalog bounds; it does not reason.** The LLM generates hypotheses, selects among
  catalogued actions, and computes confidence. The catalog supplies the *proposable set*
  plus reversal/precondition scaffolding (including amplification-trap preconditions —
  e.g. `scale-out` carries `not(bottleneck == shared_connection_pool)` so it's dropped from
  the menu when scaling out would amplify the outage). The engine must **reject any
  `ContractRef` the model proposes that isn't in the catalog** — the autonomy boundary is
  enforced *behaviourally*, not hoped.
- **The gate is not a shaper.** The readiness gate is a *go/no-go on emission* — a
  **conjunction of minimums** where one weak dimension (no evidence) can veto. The *risk
  shaper* (CRS → governance band) is a different concern — a graded magnitude. **Never blend
  the two.** The shaper is deferred; the seam is named so it can't fuse.
- **The Store is not the ledger.** Per-turn checkpoint memory (loop resumption) has a
  different lifetime and granularity from the `ProposalSet` audit ledger. Only the terminal
  `ProposalSet` is durable audit.

---

## Loop invariants (these ARE the spec)

Correctness is defined by these invariants, each backed by a test:

1. **No infra; the LLM is bounded.** Nothing mutates infrastructure; the model may propose
   **only** catalogued actions; the loop is bounded by `MaxSteps`.
2. **Digests only, never raw.** Read-only `Tool`s return an `EvidenceRef` (a one-line digest
   + a backend ref to re-fetch), never raw payloads. `EvidenceRef` has **no `Raw` field**
   and never will — raw data cannot enter the conversation `[]Message`.
3. **The catalog bounds; it does not reason.** The engine rejects any `ContractRef` not in
   the catalog (`TestPropose_RejectsACandidateOutsideTheCatalog`).
4. **The set is the audit unit.** The whole ranked `ProposalSet` is emitted and recorded —
   the trade-off *is* the artifact, not just the chosen action.
5. **The gate is a conjunction of minimums** — `budget ∧ dedup ∧ evidence`, never an
   average. One weak dimension must be able to veto. Zero policy/shaping/authority.
6. **Dedup.** An open `ProposalSet` for the same fingerprint suppresses a new one;
   suppressed means recorded but NOT delivered. Dedup filters to the open/proposed phase so
   a closed set can't suppress a live one.
7. **Frozen evidence.** The `SAO` the loop reasoned over is snapshotted into the emitted
   `ProposalSet` (`SAOSnapshot.Version > 0`); the audit trail is frozen, not dangling.
8. **Checkpoint or halt.** Each turn is checkpointed to the `Store` before the next
   iteration; a checkpoint error halts the run (re-running is safe — proposing mutates no
   infra).

> **Two budgets, two homes:** the **loop budget** (`MaxSteps` on the `Engine`, terminating
> the reason loop) and the gate's **decision/error-budget headroom** (`GateResult.BudgetOK`
> — is there room to act / are we not flapping?). Different fields; don't conflate them.

---

## The five belief-formation defences (why clank exists)

clank's value proposition is **confidence as a first-class, dynamic, calibration-checkable
value** — the defence against **hallucination propagation**: a cheap wrong belief, formed by
the reasoner, compounding through scoring and memory. (The canonical trap: an old "similar
incident, fixed by restarting X" retrieved from the case base and applied *after topology
changed*, producing a brief false improvement recorded as success that increments
confidence.)

These are **core requirements — tested, not optional**. Without them the model's confidence
is decorative:

1. **≥2-source corroboration floor** *(causal scorer / loop)* — a `historical_alignment`
   match retrieved from the case base cannot raise `Likelihood` or the model's confidence
   alone; it needs live-telemetry corroboration first (`LiveCorroborated`).
2. **Freshness-decay** *(causal scorer)* — historical alignment decays by topology-staleness
   since the referenced incident (the half-life is a `GatePolicy` param, passed in — not a
   buried literal).
3. **Negative-signal check** *(causal scorer / loop)* — a predicted-but-absent indicator
   **decrements** `Likelihood`. Absence of an expected indicator is evidence *against*, not
   silence.
4. **`partial_non_converging` outcome** *(`ProposalStatus.Outcome` enum)* — a partial
   improvement that doesn't converge must **decrement** the prior, never increment it. The
   enum state exists in the schema now; unpopulated in v1.
5. **Forced live-telemetry citation** *(gate `EvidenceOK`)* — a `ProposalSet` citing only
   `change_snapshot` / `historical_alignment` with no fresh live citation **fails the gate**.
   `EvidenceRef.Live` / `CausalScore.Rationale` is the citation carrier.

**The SLO canary:** rising **Calibration Error (CE)** is the steady-state signature of
hallucination drift; **Grounding Rate** (% of reasoning traceable to raw signals) is the
direct LLM-era SLO for this loop. Both are schema-ready, data-pending in a propose-only v1.

---

## Boundary objects & vocabulary

The vocabulary is small and fixed — **do not invent new nouns.**

**Data types:** `SignalDetection` (rattle's), `SAO` (+ `SignalSnapshot`,
`TopologySnapshot`, `ChangeSnapshot`, `ChangeEvent`), `FailureClass` (closed enum — the
model's leading hypothesis, *not* a rules table), `Hypothesis`, `EvidenceRef`,
`ActionContract` (+ `Precondition`), `Candidate`, `CausalScore`, `GateResult`, `ProposalSet`
(+ `ProposalStatus`, `RankingRationale`), `GovernanceLevel`.

**LLM-loop types:** `Model`, `Message`, `Completion`, `ToolCall`, `ToolSpec`, `Tool`,
`Turn`, `Store`, `MaxSteps`.

**Seams (interfaces):** `Intake`, `Model`, `Tool`, `Catalog`, `CausalScorer`, `Ranker`,
`Gate` (impl `ReadinessGate`), `Store`, `ProposalLog`, `ProposalSink`, plus the `Engine`
struct that wires them.

**Boundary objects** cross a plane edge (and, in Phase 2, graduate to CRDs). Engine
*internals* (`SAO`, `Candidate`, `CausalScore`, `Turn`, `Message`) stay in memory:

| Object | Owner | Role | Direction |
|---|---|---|---|
| `SignalDetection` | **rattle** (imported) | fat divergence snapshot: signal + topology + traffic + dual-axis impact; fingerprinted | **in**, read-only |
| `SAO` | clank | versioned evidence bundle the loop reasons over | internal |
| `ActionContract` | authored catalog | static action template keyed to (failure_class × tier); the **autonomy boundary**; preconditions encode amplification traps | input |
| `GatePolicy` | authored | threshold matrix + causal/ranking weights; read each reconcile | input |
| `ProposalSet` | **clank** | ranked candidate set; **the audit unit**; carries SAO snapshot, hypotheses, gate result, outcome | **out** |

`ProposalSet` **is the Candidate Action boundary object** — and **the set, not the chosen
action, is the audit unit**. "Why X?" answers as "considered N actions, ranked them thus,
here's the trade-off." It carries the frozen `SAO` snapshot, the `FailureClass`, the
`Hypotheses` (leading + competing, weighted), the `GateResult`, the full ranked `Proposals
[]Candidate`, the `Recommended` (rank-1) ID, the `RankingRationale`, and `ProposalStatus`.

---

## Repository layout & current state

```
thump/
├── cmd/
│   ├── clank/main.go        # clank thin entry
│   ├── hiss/main.go         # hiss thin entry
│   ├── rattle/main.go       # rattle thin entry
│   └── thump/main.go        # thump thin entry
├── internal/
│   ├── signal/              # rattle⟷clank contract (Detection, Severity, BlastRadius)
│   ├── proposal/            # clank⟷hiss contract (Set, Candidate, Hypothesis)
│   ├── rattle/              # Signal Plane (detectors, reconcile, enrichment)
│   ├── clank/               # Reasoning Plane (intake, reason loop, causal scorer, ranker)
│   ├── hiss/                # Governance Plane (authority, decision log, policy)
│   └── thump/               # Execution Plane (future)
├── Makefile · .golangci.yml
└── README.md · CLAUDE.md
```

**Current state (2026-07-03).**
- **clank (Reason)**: Phase 1 binary is **done** (W0→W7). The full reason loop, pure modules, belief-formation defences, and autonomy boundary are green.
- **rattle (Detect)**: **Done** (W0→W9). v1 and v2 are landed. Three detectors (burn-rate, multi-signal, historical-envelope) are wired.
- **hiss (Govern)**: **Active Front** (W0→W6). W0-W5 landed (`Decision`, `Authority.Evaluate`, `DecisionLog`, `Transport`). Wave 6 (the keyless three-beat seam test) is the current final wave.
- **thump (Act) & click (Learn)**: Named but not yet built.

(Note: Each beat carries its own wave plan numbered independently — e.g., rattle's W0-W9 vs clank's W0-W7 vs hiss's W0-W6.)

---

## Building & testing

| Command | What it does |
|---|---|
| `make run-clank` / `run-rattle` / `run-hiss` / `run-thump` | run one beat (`go run ./cmd/<beat>`) |
| `make build` | build all four beats to `bin/` (injects version/commit/date ldflags, `-trimpath`) |
| `make ci` | full local CI: fmt-check → vet → lint → test → build |
| `make test` / `make race` | tests, with `-race` |
| `make coverage` | coverage profile + total |
| `make vulncheck` | govulncheck over deps (separate security gate, not part of `make ci`) |
| `make images` | multi-arch (`linux/amd64,linux/arm64`) container per beat, pushed to `$(REGISTRY)`, SBOM + SLSA provenance attached to the manifest |
| `make sign-images` | keyless (Sigstore/Fulcio) cosign signature over each image `images` just pushed |
| `make sbom-binaries` / `sign-binaries` | SBOM + keyless blob-signature for the `bin/` outputs (no release channel consumes these yet) |
| `go test ./internal/clank -run TestGate -v` | run a single test |
| `gotestdox ./...` | read test names back as a spec sentence list |

**Definition of done:** `make ci` green (fmt-check → vet → lint → test `-race` → build);
each module a green claim; the five belief-formation defences green; the autonomy boundary
behavioural; the loop invariants green; `gotestdox ./...` reads as a clean spec;
`make vulncheck` clean; none of the deferred things built.

---

## How we build: test-first, wave by wave

The build is test-first (red→green), held loosely (see [Contributing](#contributing)). Tests
live in the **external** `clank_test` package so they exercise the API as a real caller
would. Suggested order — the pure modules are independent cold-starts, then the keystone:

- **W0 Gate** · **W1 Catalog** (autonomy boundary) · **W2 Causal scorer** (+ belief
  defences) · **W3 Ranker** · **W4 Ledger + Store** — all pure / cold-start, start anywhere.
- **W5 SAO intake** (fake sources) → **W6 Reason-loop Engine** (the keystone — wire it all,
  driven by a fake `Model` + fake sources + fake sink: "the honest version of TDD an agent
  loop") → **W7 MarkdownSink** (an `Example…` with a `// Output:` block).

Conventions that keep tests sharp:

- **Name tests as falsifiable claims** (Action·Condition·Expectation):
  `TestGate_RejectsWhenNoEvidence`, `TestCausalScorer_TopologyOutweighsRecency`,
  `TestPropose_RejectsACandidateOutsideTheCatalog`. `gotestdox ./...` then reads the suite
  back as a spec.
- **Failure messages name the user-visible failure** plus `cmp.Diff(want, got)` — not
  `want %v got %v`.
- **The only doubles you need** are the `Model` (a scripted sequence of `Completion`s), the
  **sources** (behind `Intake`), and the **sink**.

---


---

## Trajectory: Phase 1 is done, Phase 2 is here

- **Phase 1 — the binary (done).** The test-first LLM reason loop (`clank`) is complete.
- **Phase 2 — the DRAL five-beat engine.** We are now building the full five-beat DRAL vision (rattle → clank → hiss → thump → click) as one monorepo, which will eventually graduate to independent repos/binaries decoupled by a pub-sub broker (NATS JetStream). To reflect this, the project and repository have been renamed from `clank` to `thump`.

**Phase 2 does not change Phase 1.** Whatever the trigger/delivery surface ends up being, it's a new *caller* of `clank.Engine.Propose`, not a rewrite of the reason loop.
---

## Contributing

This is a **learning project** as much as a build (the author is using it to get fluent in
Go), and the working agreement reflects that:

- **Never commit or push — the repo owner lands all commits.** Edits, tests, and `make ci`
  are fair game; the commit is always the owner's to make.
- **Hold TDD loosely.** The wave plan is a great spine, but it is deliberately *not*
  dogmatic — sometimes a test comes first, sometimes a spike, sometimes a tangent. Bring it
  back to tests when it's useful, not as a ritual.
- **Teach, don't just type.** Explain the *why* — the Go idiom at play, what a test pins —
  not only the final code.
- **Respect the seams.** The module **must-not** column is the design. A policy check in the
  gate, a raw payload in a `Message`, a recomputed fingerprint, a new noun — these are the
  regressions that matter most.

### Go house rules

- Errors: wrap with `%w`, compare with `errors.Is`/`errors.As`, combine with `errors.Join`.
  Package-level `var ErrFoo = errors.New(...)` for sentinels. Never return a typed-nil
  pointer as an `error`.
- **Accept interfaces, return structs.** Interfaces are consumer-defined.
- `context.Context` is the **first parameter**, never a struct field. No
  `context.Background()` deep in call chains.
- Run `go test -race` for concurrency; use `testing/synctest` for deterministic
  time/concurrency tests.
- Prefer stdlib: `any`, builtins (`min`/`max`/`clear`), `log/slog`, `slices`/`maps`.
- Don't guess signatures — use `go doc` or gopls.

### Service shape

- Operational output goes through the default `slog` JSON handler — no `fmt.Println`.
- Shutdown is driven by `signal.NotifyContext`; long-running work selects on `ctx.Done()`.
- **Two separate observability layers, never fused:** the **audit trail** (the versioned
  SAO, the `ProposalSet`, the hypotheses + `EvidenceRef`s + `CausalScore.Rationale`, the
  per-minimum `GateResult` booleans — answers "why did clank decide this?"; Grounding Rate
  is computed off it) and **operational telemetry** (each loop stage emits `slog` + a RED
  metric + a trace span). Instrumentation wraps the seams; it never leaks into a pure
  scorer's or the loop's logic.

---

## Source of truth

The canonical scope, architecture, and build plan live in the Obsidian vault under
`~/Documents/vault/Projects/thump/` — read them live, do not mirror them:

- `clank-readme.md` — anchor / one-page overview.
- `clank-architecture.md` — **architecture of record**: the reason loop, the module seams,
  the boundary objects, the belief-formation defences. The *what and why*.
- `clank-implementation-guide.md` — the **test-first (red→green) build walkthrough**. Every
  type as real Go in § THE CAST; each behaviour with its test and the production code it
  forces. The *how*; follow it wave by wave.
- `clank-running-notes.md` — investigation journal / decision record.
- `clank-todo.md` — the live Wave checklist (W0→W7).

Sourced from *Agentic Reliability Engineering* (ch6 four planes, ch7 incident response,
ch8 delivery, ch9–10 chaos / belief-formation defences), with build method from
*The Power of Go: Tests / Tools* and delivery/layout from *Shipping Go*.
