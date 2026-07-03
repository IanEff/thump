# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What clank is

A Go **LLM Reasoning Plane** — a *bounded reason loop* that turns one `SignalDetection`
(a detected reliability event from `rattle`, the Signal Plane — built in this same repo as
`internal/rattle`, see § rattle below) into a ranked,
deduplicated, evidence-backed **`ProposalSet`**. It **assembles** a versioned snapshot
(the SAO), then an **LLM investigates it with read-only tools**, **generates hypotheses**,
and **proposes candidate actions with dynamic confidence** — bounded by an authored action
catalog, grounded by belief-formation guardrails, ranked, and gated on readiness. It does
**not** detect (that's rattle), does **not** execute against infrastructure, and does
**not** authorize (that's the Governance Plane — now **hiss**, also built in this same repo
as `internal/hiss`, see § hiss below; clank the *package* still doesn't build it, but the
repo does).

> **The reasoning is the LLM; the catalog is its leash.** clank *is* a free-form reasoner
> — there **is** an LLM in the runtime (behind a `Model` interface, faked in tests). The
> `ActionContract` catalog is the **autonomy boundary**: the set of actions clank is
> *allowed* to propose, with reversal/success/amplification scaffolding. The LLM does the
> reasoning; the catalog fences what it may reach for. Both are load-bearing — the book's
> safety property is *nothing outside the catalogue can be proposed or executed*.

> **History — read this, it explains the shape.** clank was built as an LLM agent loop
> (2026-06-21 → 06-25), then briefly **re-cast as a deterministic scoring engine** on
> 2026-06-26 (the repo's `6c56ae5 Pre-rewrite.` commit and the `0f9e637` detour) — a
> reading that traced to an *editorial gloss in the ch7/ch8 harvest notes* ("clank is not
> a free-form reasoner… no LLM required"), **not** to the book, whose Reasoning Plane is
> unambiguously LLM-driven. **That pivot was reversed the same day.** clank is the book's
> LLM Reasoning Plane again, now **keeping every structural asset the detour produced** —
> the SAO, `ProposalSet`-as-audit-unit, the gate-vs-shaper split, and the five
> belief-formation defences. See § What changed below, and the vault's
> `clank-running-notes.md` `2026-06-26 § Reverse the deterministic pivot`.

In the four-plane agentic-reliability architecture, clank is the **Reasoning Plane**: it
**selects** (reasons over evidence, generates hypotheses, proposes + ranks candidate
actions) — it does **not permit** (authority/policy is hiss's job, the Governance Plane,
§ hiss below) and it does **not detect** (that's the Signal Plane — `rattle`,
whose `SignalDetection` is clank's input). "Selects vs. permits" is the boundary the whole
design rests on; see § The clank ⟷ rattle boundary below (the same discipline governs the
clank ⟷ hiss seam — see § hiss).

Long-running service. Module `github.com/ianeff/thump`, Go 1.26. Structured `slog` logging,
context-driven graceful shutdown.

> **Repo state (updated 2026-07-02):** clank's Phase 1 binary (W0→W7, the reason-loop
> engine) is **DONE** — `make ci` clean end to end, `Engine.Propose` runs the full bounded
> loop, `MarkdownSink` renders a `ProposalSet`. **rattle** (the Signal Plane,
> `internal/rattle`) is now **also wave-complete** — v1 (W0–W4b) + v2 (W4.5–W9) all landed,
> both binaries green under one `make ci`; see § rattle below for the detail and what's next.
> **hiss** (the Governance Plane, `internal/hiss`) is now **the active front** — Wave 0
> ratified (**wrap**: clank keeps the gate, the policy migrates), Waves 1–5 landed
> (`internal/proposal` leaf extraction, `Decision`+`Auditable`, `Authority.Evaluate`,
> `DecisionLog`, `Transport`+`Main`); Wave 6 (the keyless three-beat seam test) is the
> last wave — see § hiss below for its current, specific blocker. clank's Phase 2 shape is
> **no longer an open fork** — the DRAL five-beat monorepo vision has won in practice
> (hiss's existence is the proof); see § hiss and the Trajectory section below. **The
> authoritative design is in the vault — build from there** (vault path moved, see
> § Source of truth below).

## How we work together (read this)

This is a **learning project** as much as a build: Ian is using it to learn Go, and will lean
on Claude heavily as a pairing partner to figure out *how* to implement the code. So:

- **Never commit or push. This is Ian's repo to land.** Do not run `git add`, `git commit`,
  `git push`, or otherwise check anything in here — not even when work is green. Edit files,
  run tests/`make ci`, and stop. Ian owns the history; offer to stage or describe a commit if
  asked, but the commit is always his to make.
- **Teach, don't just type.** Explain the *why* — the Go idiom at play, what a test is
  pinning, what a failure proves — not only the final code. Surface the reasoning behind the
  move so the lesson sticks.
- **Hold TDD loosely.** The implementation guide is written red→green, and that's a great
  spine, but we are deliberately **not dogmatic** about it. Ian has ADHD and follows energy
  and curiosity — sometimes that means writing a test first, sometimes spiking the code to see
  it work, sometimes jumping waves or chasing a tangent. Follow where the interest goes; bring
  it back to tests when it's useful, not as a ritual.
- **Go at the user's pace, not ahead of it.** Don't race off and implement multiple waves
  unprompted. Do a chunk, talk about it, leave room for Ian to drive.
- **Name the concept** (table tests, pure functions, interface seams, `synctest`, escape
  analysis, "TDD an agent loop with a fake `Model`") so the lesson generalizes beyond this repo.
- Building something awesome is the goal; Ian getting fluent in Go is the point — and it's a
  learning process for both of us. Optimize for that, not for process purity. 🤖

## Source of truth: the Obsidian vault

The canonical scope, architecture, and build plan live in the vault at `~/Documents/vault`.

> **Vault reorg (2026-07-01):** the per-beat doc folders got consolidated — clank's,
> rattle's, and hiss's docs (and the new project-wide anchor docs) all now live together in
> one folder, **`~/Documents/vault/Projects/thump/`** — *not* separate `Projects/clank/` /
> `Projects/rattle/` / `Projects/hiss/` folders. `thump` is the umbrella name for the whole
> five-beat engine (see § hiss and Trajectory below); the folder took that name even though
> the repo/module haven't been renamed yet (and won't be until the five-beat shape is
> stable — vault `beat-roadmap.md` §4). If you go looking for `Projects/clank/` and it's not
> there, this is why — check `Projects/thump/` first. Read every doc below **live** — do not
> mirror them into the repo:

- `thump-readme.md` — the **new top-level anchor** for the whole five-beat engine
  (rattle → clank → hiss → thump → click). Read this first, then drill into the beat you're
  touching. Points at `thump-charter.md` (the adherence contract) and `beat-roadmap.md` (the
  build sequence, what's open at each step).
- `clank-readme.md` — clank-specific anchor / one-page overview.
- `clank-architecture.md` — **architecture of record**: the reason loop, the module seams,
  the boundary objects, the belief-formation defences, the on-disk layout, and the line
  between built-now and deferred. The *what and why*.
- `clank-implementation-guide.md` — the **test-first (red→green) build walkthrough**. Every
  type is defined as real Go in § THE CAST; each behaviour has its test code and the
  production code it forces into existence; the reason loop is driven by a **fake `Model`**.
  The *how*; follow it wave by wave.
- `clank-running-notes.md` — investigation journal; where open decisions get pinned (see the
  `2026-06-26 § Reverse the deterministic pivot` entry for the reversal).
- `clank-todo.md` — the live checklist (Waves W0→W7, claim by claim).

A canonical scope doc is destined for this repo at `docs/decision-engine-scope.md` (not yet
written — Ian's to author). The vault module path is `github.com/ianeff/thump` (matches
`go.mod`); if you spot a stale `github.com/ianeff/clank` or `github.com/ifurst/clank`
anywhere, the real path wins.

## rattle — where it lives, and current focus

**rattle is being built in this repo, and its wave plan is now complete (2026-07-01).** The
locked decision (vault
`clank-running-notes.md`, `2026-06-30 § DRAL beat names locked`, "Monorepo for now — and
rattle goes in *here*, not its own repo") is: rattle lives at `internal/rattle` inside this
same `clank` module, with its own `cmd/rattle/main.go` entry — **not** the standalone
`~/projects/go/rattle` some vault docs still describe. Rationale: the beats co-evolve
wave-by-wave and need to be presented as one system; separation of concerns is enforced at
the **package** boundary, not the repo boundary, until the contracts stabilize. **hiss**
(Govern) is no longer "not-yet-built" — it's the active front, built the same way, at
`internal/hiss`; see § hiss below. `thump` (Act) / `click` (Learn) are still named + zero
code. A pub-sub split into independent repos/binaries over a broker (NATS JetStream is the
leading pick) is still the named **Phase-2 target**, not current work — see Trajectory below.

rattle has its **own wave plan, numbered independently of clank's** (W0–W9, vs. clank's
W0–W7) — don't conflate them when a branch or wave number comes up. Its docs live in the
vault too, in the same shared `Projects/thump/` folder as clank's and hiss's (see § Source
of truth above for the 2026-07-01 reorg) — read live, same discipline as clank's docs, do
not mirror into the repo:

- `rattle-readme.md` — anchor / one-page overview.
- `rattle-implementation-guide.md` — the test-first build walkthrough, THE CAST, and the
  wave-by-wave claim code (Waves 0–4b = v1, Waves 4.5–9 = v2 — all now landed).
- `rattle-running-notes.md` — investigation journal.
- `rattle-todo.md` — the live checklist by wave.

**Known stale spot:** `rattle-readme.md` and `rattle-todo.md` still describe rattle as a
future standalone repo in places — that's flagged as a backlog item in `rattle-todo.md`
itself, not an in-progress mistake. Trust the monorepo decision above over those passages.

**Current progress (as of 2026-07-01, merged to `main` via PR #15):** rattle's **entire
wave plan is landed** — v1 (W0–W4b) *and* v2 (W4.5–W9), `make ci` green end to end. All
three pure detectors are wired into `Reconciler.Reconcile` as OR branches — burn-rate
acceleration (W0), multi-signal correlation (W5), and the historical-envelope detector (W6:
`EnvelopeDetector` + `BaselineSource`, `detectorType: "historical_envelope_breach"`). On top
of that: the W4.5 `Fires`/`Detect` shim is retired (one `Detect` per window, `(detectorType,
accel)` threaded into `SignalFor`); the W7 signal contract (`SignalContract` — freshness gate
+ attenuate-don't-suppress) gates the top of `Reconcile`; **W8 enrichment is now wired**
(`Reconciler.TopologySource`/`TrafficSource` fields → `EnrichSeverity`/`EnrichTopology`/
`EnrichTraffic` on every fired detection — closing the earlier "built-but-not-called" open
item); and W9's `Envelope` interface refactor (`envelope.go` `type Envelope interface`;
`fingerprint` + `SignalFor` now take an `Envelope`, not an `SLO`) is done. Next work is Ian's
call — the v2 plan is exhausted; likely candidates are wiring real Prometheus/Sloth sources,
or reconciling the stale readme/todo passages flagged below.

rattle and clank couple through exactly one shared package, **`internal/signal`**
(`Detection` + the `Severity`/`BlastRadius`/`Divergence` value objects) — `rattle/signal.go`
already imports it directly and constructs `signal.Detection` values in `SignalFor`; this is
exactly the monorepo case the package doc comment anticipates ("when rattle joins the
codebase it imports this package directly"). The edge stays one-directional
(`rattle`/`clank` → `signal`, never back). Beyond that seam, `rattle` and `clank` are two
independent binaries in one module (`cmd/rattle`, `cmd/clank`) — no direct function calls
between them; see § On-disk layout below.

## hiss — where it lives, and current focus

**hiss is the Governance Plane** — "is the agent allowed to do this, right now?" It reads
one delivered `ProposalSet` and emits exactly one `Decision` (approved / escalate /
rejected — rejection is an audit record, never silence). Same monorepo pattern as rattle:
`internal/hiss` + its own `cmd/hiss/main.go` entry, own wave plan numbered independently
again (**W0–W6**, vs. clank's W0–W7 and rattle's W0–W9). Docs: `hiss-implementation-guide.md`
in the shared `Projects/thump/` vault folder (see § Source of truth above).

**Wave 0 is RATIFIED (2026-07-01): wrap, not extraction.** clank **keeps**
`ReadinessGate` — its evidence leg is belief-formation defence 5 (§ The loop contract
above), a native-to-the-reasoner check that shouldn't move planes; its budget leg is
already vestigial (`budgetOK := true` — the real budget check is the engine's `MaxSteps`).
What **does** move: the *policy* — clank's `GatePolicy.Threshold` (per-tier×class
confidence floors that clank's own gate never actually read) migrates to
`hiss.Policy.Floors`, because I-3 (policy lives in one place) says policy data sitting in
the reasoner with no policy holder is mid-rot, not because the gate itself belongs in
hiss. `CausalWeights` (scorer tuning, not policy) stays in clank. This is the book's
grammar: Reasoning *selects*, Governance *permits* — a verdict pass over the winning
recommendation, not a relocated readiness check.

**Boundary objects:** `ProposalSet` crosses in (clank's, extracted in Wave 1 into a shared
leaf package `internal/proposal` — `clank.ProposalSet` is now a **type alias** for
`proposal.Set`, kept for compatibility; hiss imports `internal/proposal` directly, never
`internal/clank`, so the edge stays one-directional and acyclic). `Decision` crosses out —
hiss-owned, carries `Verdict`/`Reasons`/`RequestedBand`/`GrantedBand`/`FloorApplied`/
`PolicyVersion`/`EvaluatedAt`, plus a born-auditable `Auditable() error` invariant method
every `Evaluate` output is tested against.

**Current progress (as of 2026-07-02):** Waves 1–5 are landed — `internal/proposal` leaf
extraction (Wave 1, zero behavior diff, aliases in clank), `Decision` + `Auditable` (Wave
2), `Authority.Evaluate` (Wave 3 — confidence floor, authority ceiling with absence-is-
lowest + unparseable-escalates, the I-12 irreversibility veto, freeze windows, I-7
never-mutates/never-re-ranks, ungated-input rejection, and the golden happy path all
green), `DecisionLog` (Wave 4, append-only, mutex'd, `make race` clean), and
`Transport`+`Main` (Wave 5 — filesystem-as-fake `Tick(ctx)` poll pass, poison-pill
quarantine that survives and doesn't delete evidence, the three `Main` branches).

**Wave 6 — the last wave — is in progress, with a specific known blocker.** The claim is
`TestSeam_ClankDeliveryGovernsToAnApprovedDecision`: a real, scripted `clank.Engine.Propose`
run delivers a `ProposalSet`, and `hiss.Authority.Evaluate` governs it to `approved`,
deterministically, keyless (no `ANTHROPIC_API_KEY`), in `make ci`. The guide is explicit
that this test **must live at `internal/clank/hiss_seam_test.go`, `package clank_test`**
— not in `cmd/hiss` — because it reuses the unexported test kit (`fakeModel`,
`proposeArgs`, `newTestEngine`, `captureSink` from `engine_test.go`; `sigBurnAccel()` from
`intake_test.go`) that only same-package files can see. There's already a precedent for
this exact shape on disk: `internal/clank/seam_test.go` (the earlier rattle→clank seam,
W10c) does the identical reuse trick, importing a third internal package (`rattle`) with no
cycle trouble. As of this writing an early draft of the Wave 6 test exists but is
misplaced (`cmd/hiss/hiss_seam_test.go`, `package hiss_test` — which doesn't even compile,
since `cmd/hiss` is `package main`); moving it to the right file + package is the whole
remaining fix, no logic changes needed. One thing the guide flags as a risk turned out
**not** to apply here: `engine.go`'s `propose` handler does a plain `json.Unmarshal` into
the full `ProposalSet` struct (no slimmed-down schema), so `ReversalPath` already
round-trips through the tool-call JSON — the "reversal-path schema-stripping trap" the
guide warns about is moot, verified on disk.

**Optional follow-up, not required for Wave 6** (the guide lists it as a separate,
green-to-green DoD line): drop the dead `_ GatePolicy` param from
`ReadinessGate.Evaluate` (`gate.go`), delete the never-set `GateResult.ThresholdApplied`,
rename the residual `GatePolicy` → `ScoringWeights`.

## Architecture (the one-sentence shape)

One `SignalDetection` comes in → clank assembles a versioned SAO, then an **LLM reason loop**
investigates it with read-only tools (bounded by an authored action catalog, grounded by
belief-formation guardrails) and proposes hypotheses + candidate actions with confidence; a
deterministic ranker orders them and a readiness gate decides emission → one ranked
`ProposalSet` comes out, recorded and deduped, **the set itself the audit unit**. There **is**
an LLM (behind `Model`, faked in tests). Nothing touches infrastructure.

`Engine.Propose(ctx, SignalDetection) (ProposalSet, error)` runs the loop:

```
SignalDetection (rattle, read-only)
  ① INTAKE       assemble the SAO (Option B — clank does the reading): SignalSnapshot +
  │               TopologySnapshot + ChangeSnapshot, versioned
  ② REASON LOOP  seed []Message from the SAO, then bounded loop (≤ MaxSteps):
  │               Model.Complete(msgs, tools) → checkpoint each turn (Store)
  │                 ├─ telemetry tool  → run read-only, append the DIGEST (never raw), loop
  │                 ├─ case-base tool   → retrieve similar past incidents (Learn edge), loop
  │                 ├─ "propose"        → model emits hypotheses + candidate actions (drawn
  │                 │                     from the catalog) + per-hypothesis confidence → exit
  │                 └─ "insufficient" / no tool calls → no_action → exit
  ③ GROUND       belief-formation guardrails on what the loop may believe: ≥2-source floor ·
  │               freshness-decay · negative-signal checks
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
so a crashed run resumes. Ranking and the gate run **once** on the formed set, after the loop
exits. Intake reads sources, the loop calls the `Model` and tools, emit writes — everything
between (causal scorer, ranker, gate) is a pure, table-testable function.

The vocabulary is small and fixed — do not invent new nouns. **Data:** `SignalDetection`
(rattle's — reproduced in clank's `internal/signal` package as `signal.Detection`, the
unstuttered local name for the same contract), `SAO` (+ `SignalSnapshot`, `TopologySnapshot`,
`ChangeSnapshot`, `ChangeEvent`),
`FailureClass` (closed enum — the model's leading hypothesis, *not* a rules table),
`Hypothesis`, `EvidenceRef`, `ActionContract` (+ `Precondition`), `Candidate`, `CausalScore`,
`GateResult`, `ProposalSet` (+ `ProposalStatus`, `RankingRationale`), `GovernanceLevel`. **The
LLM-loop nouns (back in scope):** `Model`, `Message`, `Completion`, `ToolCall`, `ToolSpec`,
`Tool`, `Turn`, `Store`, `MaxSteps`. **Seams (interfaces):** `Intake`, `Model`, `Tool`,
`Catalog`, `CausalScorer`, `Ranker`, `Gate` (impl `ReadinessGate`), `Store`, `ProposalLog`,
`ProposalSink`, plus the `Engine` struct that wires them. See `clank-implementation-guide.md`
§ THE CAST for the exact definitions.

`ProposalSet` **is the Candidate Action boundary object** — and **the set, not the chosen
action, is the audit unit**. "Why X?" answers as "considered N actions, ranked them thus,
here's the trade-off." It carries the frozen `SAO` snapshot, the `FailureClass`, the
`Hypotheses` (leading + competing, weighted — the reasoning chain), the `GateResult`, the full
ranked `Proposals []Candidate`, the `Recommended` (rank-1) ID, the `RankingRationale`, and
`ProposalStatus`. Each `Candidate` carries its own *hypothesis* `Confidence` and a
`GovernanceLevel` **band** — a graded *request*, never a verdict.

### The clank ⟷ rattle boundary (do not blur)

clank is the **Reasoning Plane**; `rattle` is the **Signal Plane**. The safety of the whole
design rests on this seam holding. Three rules:

1. **The `SignalDetection` is rattle's, not ours.** clank consumes it read-only and **trusts
   it** — it never recomputes the fingerprint (assigned by rattle, the dedup key), never
   re-judges signal trustworthiness/significance. The `SignalDetection` definition in the
   vault guide is reproduced *for reference*; rattle owns it. **clank imports rattle's type;
   it never defines it** (declaring it as a `+kubebuilder:object` in clank's repo would
   silently move Signal-Plane ownership into Reasoning — don't).
2. **Two confidence numbers, never one field.** *Signal-strength* confidence ("is this
   real?") lives on `SignalDetection.Divergence.Confidence` and is **rattle's** — clank reads
   it, never sets it. *Hypothesis* confidence ("how sure of this fix?") lives per `Candidate`
   and is **clank's**, computed by the reason loop. Don't conflate them.
3. **clank selects; it does not permit.** The gate decides whether a `ProposalSet` is worth
   **emitting**, NOT whether an action is authorized. The gate has **zero policy** in it — no
   criticality tier, no error-budget check, no confidence threshold. Each `Candidate` carries
   a `GovernanceLevel` band (a *request*); a Governance Plane clank does **not** build converts
   the band to allow/deny. Any `if criticality…`, `if error_budget…`, or
   `if confidence < threshold` inside clank is the seam that rots first.

**Two-axis impact, never collapsed:** rattle hands clank **severity** (how bad — a metric
property) *and* **blast radius** (how broadly exposed — a who/what property) as independent
axes, each with its own velocity. The ranker reads both; it never merges them into one
"badness" number.

### The loop contract + belief-formation defences (these ARE the spec)

Two things define correctness. First, the **loop invariants**:

1. **No infra; the LLM is bounded** — nothing mutates infrastructure; the model may propose
   **only** catalogued actions (the autonomy boundary), and the loop is bounded by `MaxSteps`.
   The reasoning is the LLM, fenced by the authored catalog.
2. **Digests only, never raw** (Inv. 1) — read-only `Tool`s return an `EvidenceRef` (a one-line
   digest + a backend ref to re-fetch), never raw payloads. `EvidenceRef` has **no `Raw` field**
   and never will; raw data cannot enter the conversation `[]Message`.
3. **The catalog bounds; it does not reason** — the LLM generates hypotheses, selects among
   catalogued actions, and computes confidence; the catalog supplies the *proposable set* +
   reversal/precondition scaffolding (incl. amplification-trap preconditions). The engine must
   **reject any `ContractRef` the model proposes that isn't in the catalog** — the autonomy
   boundary is enforced behaviourally, not hoped.
4. **The set is the audit unit** — the whole ranked `ProposalSet` is emitted and recorded,
   not just the chosen action; the trade-off IS the artifact.
5. **The gate is a conjunction of minimums** — `budget ∧ dedup ∧ evidence`, never an average.
   One weak dimension (no evidence) must be able to veto. The gate holds **zero**
   policy/shaping/authority.
6. **Dedup** — an open `ProposalSet` for the same fingerprint suppresses a new one; suppressed
   means recorded but NOT delivered. Dedup filters to the open/proposed phase so a closed set
   can't suppress a live one.
7. **Frozen evidence** — the `SAO` the loop reasoned over is snapshotted into the emitted
   `ProposalSet` (`SAOSnapshot.Version > 0`); the audit trail is frozen, not dangling.
8. **Checkpoint or halt** — each turn is checkpointed to the `Store` before the next iteration;
   a checkpoint error halts the run (re-running is safe — proposing mutates no infra). The
   `Store` is loop memory, **not** the proposal ledger (different lifetime + granularity).

Second — and this is **why clank exists** — the **five belief-formation defences** (ch9 §7.7).
clank's value prop is *confidence as a first-class, dynamic, calibration-checkable value*: the
defence against **hallucination propagation** (a cheap wrong belief, formed by the reasoner,
compounding through scoring/memory — e.g. an old "similar incident, fixed by restarting X"
retrieved from the case base and applied after topology changed). These are native to the LLM
case and are **core requirements, tested, not optional** — without them the model's confidence
is decorative:

1. **≥2-source corroboration floor** (causal scorer / loop) — a `historical_alignment` match
   retrieved from the case base cannot raise `Likelihood` or the model's confidence alone; it
   needs live-telemetry corroboration first (`LiveCorroborated`).
2. **Freshness-decay** (causal scorer) — historical alignment decays by topology-staleness
   since the referenced incident (decay curve / half-life is a `GatePolicy` param).
3. **Negative-signal check** (causal scorer / loop) — a predicted-but-absent indicator
   **decrements** `Likelihood`; absence is evidence *against*, not silence.
4. **`partial_non_converging` outcome** (`ProposalStatus.Outcome` enum) — a partial
   improvement that doesn't converge must **decrement** the prior, never increment it. The
   enum state exists in the schema now; unpopulated in v1.
5. **Forced live-telemetry citation** (gate `EvidenceOK`) — a `ProposalSet` citing only
   `change_snapshot` / `historical_alignment` with no fresh live citation **fails the gate**.
   `EvidenceRef.Live` / `CausalScore.Rationale []string` is the citation carrier.

**The SLO canary:** rising Calibration Error (CE) is the steady-state signature of
hallucination drift; **Grounding Rate** (% of reasoning traceable to raw signals) is the direct
LLM-era SLO for this loop. Both are schema-ready, data-pending in a propose-only v1.

### Deliberately NOT built (do not build or test these — a test invites building it)

- **The real `Model` client** — one fake `Model` (a scripted sequence of `Completion`s)
  drives every test; the real provider + model-id is a repo-code decision (Ian's), deferred
  behind the `Model` interface. No token streaming, no multi-provider SDK.
- **A Governance plane / any authority decision, inside `internal/clank`** — clank emits a
  `GovernanceLevel` band *request* and stops; no criticality, error-budget, change-window,
  or confidence-threshold check anywhere in this package. (This is scoped to the package,
  not the repo — hiss, § hiss above, now builds exactly this, in `internal/hiss`. Don't let
  hiss's existence tempt authority logic back into `internal/clank`.)
- **The risk *shaper* (CRS)** — the `change-risk-score` scalar, its normalizers, and the
  band map. `GovernanceLevel.Band` exists; its *computation* is parked until a
  Governance/Execution layer. Never fuse the gate (readiness) with the shaper (graded risk).
- **Signal validity / significance / fingerprinting / topology+traffic observation** —
  rattle's job; clank trusts the inbound `SignalDetection` and copies its fingerprint.
- **The delivery surface** — change-intent, the metric/cohort/timewindow registries, the
  Test-Agent / `ValidationState` / `Envelope`. Mostly rattle's; out of scope.
- **The learning-loop *writes*** — the case base is *read* in v1 (the `casebase` retrieval
  tool, stubbed source); *writing* it — similarity store, confidence calibration,
  effectiveness ratings, `GatePolicyPatch` — is deferred. `ProposalSet.Status.Outcome` exists
  but **nothing populates it** in v1.
- **`parallel-decision`** — two independent reasoning chains agreeing before emit; a
  governance primitive against confident-wrong, named but deferred.
- **Real source wiring** (ArgoCD sync events for the change source; the declared topology
  graph; real telemetry / case-base backends) — arrives via interface, **faked** in tests.
  **Postgres** `ProposalLog` / `Store` — in-memory only.

## What changed (the 2026-06-26 reversal — read if you remember the deterministic design)

For one day, clank was re-cast as a **deterministic structured-scoring engine**: "no LLM in
the runtime," the pipeline pure Go (lookup + instantiation + scoring + ranking), a rules-based
`Classifier`, an `instantiate` stage, no `Model`/`Tool`/`Store`/`Turn`. **That reading is
superseded** — it traced to an editorial gloss in the harvest notes, not the book, and was
**reversed the same day**. If your memory of this project says "no LLM," "deterministic scoring
engine," "the reasoning is in the catalog not an LLM," a `Classifier` rules table, or a
`classify.go`/`instantiate.go` seam — **that is the superseded detour.** The current design is
the LLM reason loop above.

**What the reversal kept (the detour wasn't wasted):** the SAO, the `ProposalSet`-as-audit-unit,
the gate-vs-shaper split, the readiness gate (budget ∧ dedup ∧ evidence), the dedup ledger, and
the five belief-formation defences all carried over intact — they sit *more* naturally on the
LLM case than the deterministic one. **What came back:** the `Model`/`Tool`/`Store`/`Turn`/
`Message`/`Completion`/`ToolCall` vocabulary and the bounded loop. **What's gone:** the
rules-based classifier and the separate instantiate stage — `FailureClass` is now the model's
output, and `Candidate`s come from the model's `propose` call (validated against the catalog),
not a deterministic instantiation step.

**On "budget":** there are now **two budgets, two homes** — the **loop budget** (`MaxSteps` on
the `Engine`, terminating the reason loop) and the gate's **decision/error-budget headroom**
(`GateResult.BudgetOK` — is there room to act / are we not flapping?). Different fields, don't
conflate them.

## Trajectory

Two phases were originally scoped for clank alone; that framing is now superseded by the
five-beat **DRAL vision** (rattle → clank → hiss → thump → click), which has moved from
"a newer, competing description" to **the vision actually being built** — hiss's Wave 0
ratification and Waves 1–5 landing (§ hiss above) is that decision made real, not just
proposed. The Kubernetes-operator plan below is **superseded, not merely in doubt** — kept
as history so the reasoning doesn't get re-derived.

- **Phase 1 — the binary (done, 2026-06-29).** The test-first LLM reason loop:
  `Engine.Propose(ctx, SignalDetection) → ProposalSet`, the pure modules + the loop green,
  the five belief-formation defences green, the autonomy boundary enforced behaviourally.
  Transport-agnostic library + a thin `cmd/clank` entry; the LLM behind a `Model` interface,
  faked in tests. The ch6/ch7 core (intake → reason loop → ground → rank → gate → emit) is
  built; the ch8 surface (gate-vs-shaper shaper, CRS, registries, delivery validation) is
  still **named but not built**.
- **~~Phase 2, original operator plan~~ — superseded.** The one-time idea: wrap the engine
  as a Kubernetes operator (controller-runtime / kubebuilder), a reconciler watching
  `SignalDetection` CRs and dispatching reason runs, boundary objects graduating to
  `api/v1alpha1`. Ian's 2026-06-29 call ("CRDs/etcd are no longer a given") is what opened
  the door the DRAL vision walked through; this plan is not the live direction. Kept here
  only so a future session doesn't rediscover and re-propose it as new.
- **Phase 2, now — the DRAL five-beat engine, monorepo for now.** Five named beats — rattle
  (Detect, done), clank (Reason, done), **hiss** (Govern, the active front — § hiss above),
  `thump` (Act, dry-run first, zero code), `click` (Learn, zero code, not a discrete
  module) — one monorepo (`internal/rattle`, `internal/clank`, `internal/hiss`, …),
  graduating to independent repos/binaries decoupled by a pub-sub broker (NATS JetStream
  the leading pick) once the seam contracts (`signal.Detection`, `proposal.Set`, `Decision`,
  and the not-yet-built `Outcome`/`Lesson`) stabilize. No CRDs or etcd in this version. The
  project's **eventual** name/module is `thump` (`github.com/ianeff/thump`) — the vault
  folder already made that move (§ Source of truth above) — but the repo/module rename
  itself is **deliberately deferred** until the five-beat shape is stable (vault
  `beat-roadmap.md` §4). Don't rename the module preemptively.

**Phase 2 does not change phase 1's pipeline.** Whatever the eventual pub-sub surface looks
like, it's a new *caller* of `Engine.Propose`, not a rewrite of the reason loop, the pure
modules, or their tests. Do not pre-build pub-sub broker scaffolding — that's still ahead of
hiss/thump/click landing, not current work.

## Working with the tests (a spine, not a cage)

clank's own Waves W0→W7 below are **complete** (kept here as the record of how the build
happened and the spirit to bring to whatever's next — new clank work extends this pipeline
rather than restarting it). rattle is mid-build on its own, separately-numbered spine
(W0–W9) — see § rattle above and `rattle-implementation-guide.md` for its wave list; the
conventions in this section (test-first where it's fun, fakes over mocks, falsifiable test
names) apply equally there.

The implementation guide lays out a test list (Waves W0→W7) written red→green, and it's a good
map of what to build and in what order. The pure modules are a gift to TDD — table tests, no
fakes, fast red→green. The reason loop (Wave 6) is integration-shaped: its "first consumer" is
a **fake `Model` returning scripted completions**, and writing that fake is what *forces* the
`Model`/`Tool` seam into a drivable shape — "the honest version of TDD an agent loop." The only
doubles you need are the **`Model`**, the **sources** (behind `Intake`), and the **sink**.
Suggested order:

- **W0 Gate** · **W1 Catalog** (autonomy boundary) · **W2 Causal scorer** (+ the
  belief-formation defences) · **W3 Ranker** · **W4 Ledger + Store** — all pure / cold-start,
  start anywhere.
- **W5 SAO intake** (fake sources) → **W6 Reason-loop Engine** (the keystone — wire it all,
  fake `Model` + sources + sink) → **W7 MarkdownSink** (`Example…` with a `// Output:` block).

Use it as a guide, not a mandate — see "Hold TDD loosely" above. When we do write tests, these
conventions keep them sharp:

- Name tests as falsifiable claims (Action·Condition·Expectation), e.g.
  `TestGate_RejectsWhenNoEvidence`, `TestCausalScorer_TopologyOutweighsRecency`,
  `TestPropose_RejectsACandidateOutsideTheCatalog` — `gotestdox ./...` then reads the suite
  back as a spec.
- Failure messages name the user-visible failure plus `cmp.Diff(want, got)` — not
  `want %v got %v`.
- Tests live in package `clank_test` (external), so they exercise the API as a real caller
  would.
- When a failing test comes first, confirming the *specific* red you predicted (not a panic or
  compile error) is what proves the test has teeth — worth doing when it matters, skippable
  when you're spiking. (The loop-budget test's red is literally a **hang** — an always-`metrics`
  script with no `MaxSteps` bound loops forever; bounding it is the green.)

## On-disk layout (one file per seam)

clank is **the `internal/clank` package, one file per seam** — the file boundaries already
express the module table, while keeping the test-first flow simple (tests in external
`clank_test`, one vocabulary). Two carve-outs are their own leaf packages, both
compiler-enforced one-directional edges:

- **`internal/signal`** — the rattle⟷clank contract surface (`signal.go`: `Detection` —
  rattle's `SignalDetection`, reproduced locally as `signal.Detection` — plus the shared
  `Severity`/`BlastRadius` value objects). Edge: `clank`/`rattle` → `signal`, never back.
- **`internal/proposal`** — the clank⟷hiss contract surface, extracted from
  `internal/clank` in hiss's Wave 1 (2026-07-01): `proposal.go` (`Set` — what
  `clank.ProposalSet` now **type-aliases**, plus `Candidate`, `Hypothesis`, `EvidenceRef`,
  `GateResult`, `ProposalStatus`, `PredictedImpact`, `ReversalPath`, `GovernanceLevel`,
  `RankingRationale`, `FailureClass` + consts, `CausalScore`) and `sao.go` (the `SAO`
  aggregate). A `leaf_test.go` (`package proposal_test`) pins its leafness by parsing
  imports — a stdlib-only tripwire against a future `internal/clank` import creeping back
  in. Edge: `clank`/`hiss` → `proposal`, never back. This is the "Sub-package splits...
  deferred" graduation the line below used to describe as future work — it already
  happened, for this one seam.

rattle has already joined (`internal/rattle`, its own file-per-detector layout —
`detector.go`, `debounce.go`, `reconcile.go`, `correlation.go`, `envelope.go`,
`contract.go`, `enrich.go`, `source.go`, `signal.go`; see § rattle above) and imports
`internal/signal` directly — no reshuffle needed, exactly the monorepo path the package doc
comment anticipated. hiss has joined the same way (`internal/hiss`, § hiss above):
`decision.go` (`Verdict`, `Band`, reason consts, `Decision` + `Auditable`), `policy.go`
(`Policy`, `Window`), `authority.go` (`Authority.Evaluate` — the whole beat, pure),
`ledger.go` (`DecisionLog`), `transport.go` (`Transport.Tick` — the poll-pass), `hiss.go`
(`Main`). Plus `cmd/hiss/main.go` (one-line shim, mirrors `cmd/clank`/`cmd/rattle`). The
`internal/clank` files: `sao.go`, `intake.go`, `model.go` (`Model`,
`Message`, `Completion`, `ToolCall`, `ToolSpec` — the LLM seam), `tools.go` (`Tool` +
read-only telemetry / case-base retrieval), `engine.go` (`Engine.Propose` — the bounded reason
loop, tool dispatch, set formation), `store.go` (`Store` + `Turn` + in-memory impl),
`catalog.go`, `causal.go`, `rank.go`, `gate.go`, `proposal.go` (now just the **type
aliases** onto `internal/proposal`, post-Wave-1 — the real definitions moved),
`policy.go` (shrinking to `CausalWeights`/`ScoringWeights` once the § hiss optional cleanup
lands — `GatePolicy.Threshold` itself already migrated to `hiss.Policy.Floors`), `sink.go`,
`ledger.go` (`ProposalLog`). Plus `cmd/clank/main.go` (thin entry: wire deps,
`signal.NotifyContext`, run) and `cmd/rattle/main.go` (rattle's own thin entry). Note there
is **no** `classify.go` or `instantiate.go` in `internal/clank` — those were the
deterministic detour; classification is now the model's output.

## Definition of done

- `make ci` is green: fmt-check → vet → lint → test (`-race`) → build. Run checks/tests
  incrementally during edits. **The `lint` step (golangci-lint, gosec on) is also the GitHub
  Actions gate** (`.github/workflows/ci.yml`, runs on every push to `main` + PRs) — a red lint
  keeps CI red even when every `go test` passes, so **"all tests green" ≠ "CI green"; run the
  whole `make ci`, not just `make test`.** Known trip: golden-file tests fire gosec G304
  (variable path) / G306 (file perms) on the `os.ReadFile`/`os.WriteFile` of the golden — the
  canonical fix is `0o600` perms on the write plus `//nolint:gosec // G304: fixed testdata path,
  not user input` on the read (see `schema_test.go`). This bit us once: the propose-schema
  golden (`43779fa`) silently red-lined CI on `main` for days before anyone noticed.
- Each module is a green claim (Gate, Catalog/autonomy-boundary, Causal scorer, Ranker,
  Ledger + Store, Intake, the reason-loop Engine, Sink), **and** the five belief-formation
  defences are green — if those aren't tested, the confidence machinery is decorative.
- The **autonomy boundary is behavioural** — a test proves the LLM cannot propose an action
  the catalog doesn't list (`…RejectsACandidateOutsideTheCatalog`).
- The **loop invariants are green** — bounded (`MaxSteps`), checkpoint-or-halt, digests-only,
  read-only tools.
- `gotestdox ./...` reads as a clean spec; each failure message names the user-visible failure.
- None of the ⛔ deferred things got built.
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
- Two *separate* observability layers, never fused: the **audit trail** (the versioned `SAO`,
  the `ProposalSet`, the `Hypotheses` + `EvidenceRef`s + `CausalScore.Rationale`, the
  `RankingRationale`, the per-minimum `GateResult` booleans — answers "why did clank decide
  this?"; Grounding Rate is computed off this trail) and **operational telemetry** (each loop
  stage emits `slog` + a RED metric + a trace span; Reasoning Latency, tool-call count/turn,
  and gate veto-rate by dimension are themselves agentic SLOs). The instrumentation wraps the
  seams; it never leaks into a pure scorer's or the loop's logic.
