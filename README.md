# clank

> A Go **LLM Reasoning Plane** — a *bounded reason loop* that turns one
> `SignalDetection` (a detected reliability event from **rattle**, the Signal Plane — built
> in this same repo as `internal/rattle`) into a ranked, deduplicated, evidence-backed
> **`ProposalSet`**.

clank assembles a versioned snapshot of an incident (the **SAO**), then lets an **LLM
investigate it with read-only tools**, **generate hypotheses**, and **propose candidate
actions with dynamic, calibration-checkable confidence** — bounded by an authored action
catalog, grounded by belief-formation guardrails, deterministically ranked, and gated on
readiness.

clank does **not** detect (that's rattle), does **not** execute against infrastructure,
and does **not** authorize (that's the Governance Plane, which clank does not build).

- **Module:** `github.com/ianeff/thump`
- **Go:** 1.26
- **Shape:** long-running service; structured `slog` logging; context-driven graceful
  shutdown.

> **The reasoning is the LLM; the catalog is its leash.** clank *is* a free-form reasoner —
> there **is** an LLM in the runtime, behind a `Model` interface and faked in tests. The
> `ActionContract` catalog is the **autonomy boundary**: the set of actions clank is
> *allowed* to propose. The LLM does the reasoning; the catalog fences what it may reach
> for. Both are load-bearing — the safety property is *nothing outside the catalogue can be
> proposed*.

---

## Table of contents

- [Where clank fits: the four-plane architecture](#where-clank-fits-the-four-plane-architecture)
- [The clank ⟷ rattle boundary (do not blur)](#the-clank--rattle-boundary-do-not-blur)
- [The reason loop](#the-reason-loop)
- [Module seams](#module-seams-one-file-per-concern)
- [Loop invariants](#loop-invariants-these-are-the-spec)
- [The five belief-formation defences](#the-five-belief-formation-defences-why-clank-exists)
- [Boundary objects & vocabulary](#boundary-objects--vocabulary)
- [Repository layout & current state](#repository-layout--current-state)
- [Building & testing](#building--testing)
- [How we build: test-first, wave by wave](#how-we-build-test-first-wave-by-wave)
- [Deliberately NOT built](#deliberately-not-built)
- [Trajectory: Phase 1 → Phase 2](#trajectory-phase-1--phase-2)
- [Contributing](#contributing)
- [Source of truth](#source-of-truth)

---

## Where clank fits: the four-plane architecture

clank is one plane of an *agentic reliability* architecture (from the book *Agentic
Reliability Engineering*). The design rests on a strict separation of four concerns:

| Plane | Project | Job | Verb |
|---|---|---|---|
| **Signal** | `rattle` *(this repo, `internal/rattle`)* | detect reliability divergences, emit a fingerprinted `SignalDetection` | *detects* |
| **Reasoning** | **clank** (this repo) | reason over evidence, generate hypotheses, propose + rank candidate actions | *selects* |
| **Governance** | *(not built)* | convert a requested governance band into allow/deny | *permits* |
| **Execution** | *(not built)* | act against infrastructure, observe outcomes | *acts* |

clank is the **Reasoning Plane**: it **selects** (reasons over evidence and proposes a
ranked set of candidate actions). It does **not permit** — authority/policy is the
Governance Plane's job — and it does **not detect** — that is rattle's. *"Selects vs.
permits"* is the boundary the entire design rests on.

Three hard lines clank never crosses:

1. **It does not detect.** `SignalDetection` is rattle's; clank trusts it.
2. **It does not execute.** It emits proposals; nothing touches infrastructure.
3. **It does not authorize.** Each proposal carries a *requested* governance band; a
   Governance plane converts it to allow/deny.

---

## The clank ⟷ rattle boundary (do not blur)

clank consumes rattle's `SignalDetection` read-only. The safety of the whole design rests
on this seam holding. Three rules:

1. **The `SignalDetection` is rattle's, not ours.** clank consumes it read-only and
   **trusts it** — it never recomputes the fingerprint (rattle's dedup key), never re-judges
   signal trustworthiness or significance. **clank imports rattle's type; it never
   defines it.** (Declaring it as a `+kubebuilder:object` in clank's repo would silently
   move Signal-Plane ownership into Reasoning — don't. The struct is currently *reproduced
   for reference* in its own leaf package `internal/signal` — the compiler-enforced contract
   seam, import-clean for rattle — until it graduates to a real import.)

2. **Two confidence numbers, never one field.**
   - *Signal-strength* confidence ("is this real?") lives on
     `SignalDetection.Divergence.Confidence` and is **rattle's** — clank reads it, never
     sets it.
   - *Hypothesis* confidence ("how sure of this fix?") lives per `Candidate` and is
     **clank's**, computed by the reason loop.

3. **clank selects; it does not permit.** The gate decides whether a `ProposalSet` is worth
   **emitting**, NOT whether an action is authorized. The gate holds **zero policy** — no
   criticality tier, no error-budget check, no confidence threshold. Each `Candidate`
   carries a `GovernanceLevel` band (a *request*); a Governance Plane converts the band to
   allow/deny. Any `if criticality…`, `if error_budget…`, or `if confidence < threshold`
   inside clank is the seam that rots first.

**Two-axis impact, never collapsed.** rattle hands clank **severity** (how bad — a metric
property) *and* **blast radius** (how broadly exposed — a who/what property) as independent
axes, each with its own velocity. The ranker reads both; it never merges them into one
"badness" number.

---

## The reason loop

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

## Module seams (one file per concern)

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
clank/
├── cmd/clank/main.go        # thin entry: wire deps, signal.NotifyContext, run
├── cmd/rattle/main.go       # rattle's own thin entry (Signal Plane binary)
├── internal/signal/
│   └── signal.go            # the rattle⟷clank contract leaf: Detection (rattle's
│                            # SignalDetection, reproduced as signal.Detection) +
│                            # shared value objects (Severity, BlastRadius). Both
│                            # clank and rattle import it; edge is clank/rattle → signal,
│                            # never back — a compiler-enforced seam.
├── internal/rattle/         # the Signal Plane — its own file-per-detector layout:
│   │                        # detector.go, debounce.go, reconcile.go, correlation.go,
│   │                        # envelope.go, contract.go, enrich.go, source.go, signal.go.
│   └── …                    # imports internal/signal directly; no calls to internal/clank.
├── internal/clank/
│   ├── sao.go               # SAO + sub-snapshots
│   ├── intake.go            # ① Intake.Assemble
│   ├── model.go             # Model, Message, Completion, ToolCall, ToolSpec (the LLM seam)
│   ├── tools.go             # Tool (read-only telemetry + case-base retrieval) + control specs
│   ├── engine.go            # ② Engine.Propose — the bounded reason loop
│   ├── store.go             # Store + Turn + in-memory impl
│   ├── catalog.go           # ActionContract + Catalog.Applicable (autonomy boundary)
│   ├── causal.go            # CausalScorer + belief-formation defences
│   ├── rank.go              # ④ Ranker
│   ├── gate.go              # ⑤ ReadinessGate
│   ├── proposal.go          # ProposalSet, Candidate, ProposalStatus
│   ├── policy.go            # GatePolicy
│   ├── sink.go              # ⑥ ProposalSink + MarkdownSink
│   └── ledger.go            # ProposalLog (dedup)
├── Makefile · .golangci.yml
└── README.md · CLAUDE.md
```

> Note there is **no** `classify.go` or `instantiate.go` — classification is the model's
> output, not a rules table. (See [history](#a-note-on-history) below.)

**Current state (2026-07-01).** clank's **Phase 1 binary is done** — the full reason loop
(`Engine.Propose`), the pure modules, the five belief-formation defences, and the autonomy
boundary are all green, `MarkdownSink` renders a `ProposalSet`, and `make ci` is clean end to
end. **rattle** (the Signal Plane, `internal/rattle`) has since been built in this same repo
and is **also wave-complete** — three detectors (burn-rate acceleration, multi-signal
correlation, historical-envelope) wired into `Reconciler.Reconcile`, plus a freshness signal
contract, detection enrichment (topology / severity / traffic), and the `Envelope`-interface
generalization. Two binaries, one module (`cmd/clank`, `cmd/rattle`), coupled only through
`internal/signal`. Run `gotestdox ./...` to read the whole suite back as a spec.

> **Note:** rattle's build carries its own wave plan (W0–W9), numbered independently of
> clank's (W0–W7) — don't conflate the two when a wave number comes up.

### A note on history

clank was built as an LLM agent loop (2026-06-21 → 06-25), then briefly **re-cast as a
deterministic scoring engine** on 2026-06-26 — a reading that traced to an *editorial gloss
in the harvest notes* ("clank is not a free-form reasoner… no LLM required"), **not** the
book, whose Reasoning Plane is unambiguously LLM-driven. **That pivot was reversed the same
day.** If you remember "no LLM," "deterministic scoring engine," a `Classifier` rules table,
or `classify.go`/`instantiate.go` — that is the **superseded detour**. The current design is
the LLM reason loop above.

The reversal kept every structural asset the detour produced: the SAO, the
`ProposalSet`-as-audit-unit, the gate-vs-shaper split, the readiness gate, the dedup ledger,
and the five belief-formation defences — they sit *more* naturally on the LLM case.

---

## Building & testing

| Command | What it does |
|---|---|
| `make run` | run the service (`go run ./cmd/clank`) |
| `make build` | build to `bin/clank` (injects version/commit/date ldflags) |
| `make ci` | full local CI: fmt-check → vet → lint → test → build |
| `make test` / `make race` | tests, with `-race` |
| `make coverage` | coverage profile + total |
| `make vulncheck` | govulncheck over deps (separate security gate, not part of `make ci`) |
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

## Deliberately NOT built

A test for these would invite building them. Do not add one.

- **The real `Model` client** — one fake `Model` drives every test; the real provider +
  model-id is a repo-code decision, deferred behind the `Model` interface. No token
  streaming, no multi-provider SDK.
- **A Governance plane / any authority decision** — clank emits a `GovernanceLevel` band
  *request* and stops; no criticality, error-budget, change-window, or confidence-threshold
  check anywhere.
- **The risk *shaper* (CRS)** — the `change-risk-score` scalar, its normalizers, and the
  band map. `GovernanceLevel.Band` exists; its *computation* is parked.
- **Signal validity / significance / fingerprinting / topology+traffic observation** —
  rattle's job; clank trusts the inbound `SignalDetection`.
- **The delivery surface** — change-intent, the metric/cohort/timewindow registries, the
  Test-Agent / `ValidationState` / `Envelope`. Mostly rattle's.
- **The learning-loop *writes*** — the case base is *read* in v1 (the `casebase` retrieval
  tool, stubbed source); *writing* it (similarity store, confidence calibration,
  effectiveness ratings, `GatePolicyPatch`) is deferred. `ProposalSet.Status.Outcome` exists
  but nothing populates it in v1.
- **`parallel-decision`** — two independent reasoning chains agreeing before emit; named but
  deferred.
- **Real source wiring** (ArgoCD sync events, the declared topology graph, real
  telemetry/case-base backends) — arrives via interface, faked in tests. **Postgres**
  `ProposalLog` / `Store` — in-memory only.

---

## Trajectory: Phase 1 → Phase 2

- **Phase 1 — the binary (done, 2026-06-29).** The test-first LLM reason loop:
  `Engine.Propose(ctx, SignalDetection) → ProposalSet`, the pure modules + the loop green,
  the five belief-formation defences green, the autonomy boundary enforced behaviourally,
  `make ci` clean. Transport-agnostic library + a thin `cmd/clank` entry; the LLM behind a
  `Model` interface, faked in tests. (Since then, **rattle** — the Signal Plane feeding this
  loop — has been built to completion in the same repo; see [current
  state](#repository-layout--current-state).)
- **Phase 2 — two competing descriptions, neither finalized.** How the engine gets triggered
  and delivered is **under active reconsideration** — check the vault's `clank-running-notes.md`
  (`2026-06-29 § The design divergence starts here`, `2026-06-30 § DRAL beat names locked`)
  for the live state before building toward either:
  - *The original operator plan.* Wrap the engine as a Kubernetes operator (controller-runtime
    / kubebuilder): a reconciler watches `SignalDetection` CRs and *dispatches* a reason run;
    the `ProposalSet` surfaces as a CR / status / event. **The contracts ARE the CRDs.** As of
    2026-06-29 Ian's call is that CRDs/etcd are "no longer a given" — this plan is in doubt.
  - *The newer DRAL vision.* Five named beats — rattle (Detect), clank (Reason), `hiss`
    (Govern), `thump` (Act), `click` (Learn) — as one monorepo *for now* (`internal/rattle`,
    `internal/clank`, …), graduating to independent repos/binaries decoupled by a pub-sub
    broker (NATS JetStream is the leading pick) once the seam contracts stabilize. No CRDs.

**Either way, Phase 2 does not change Phase 1.** Whatever the trigger/delivery surface ends up
being, it's a new *caller* of `Engine.Propose`, not a rewrite of the reason loop, the pure
modules, or their tests. (The one care: the reason loop is **not** a reconcile — it's a
long-running LLM conversation — so a reconcile would *dispatch* it, not run it inline.) Do not
pre-build operator or pub-sub scaffolding — the direction isn't picked yet.

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
`~/Documents/vault/Projects/clank/` — read them live, do not mirror them:

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
