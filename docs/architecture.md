# thump — architecture

How the engine is put together, and why it's shaped this way. If you're here to
*use* thump on your own systems, read `onboarding.md` instead — this document is
for people who want to understand or change the machine itself.

Companion docs: `invariants.md` (the rules this shape exists to make
enforceable), `design-decisions.md` (where the design knowingly departs from the
book it's built from, and why).

**The map:** [the problem](#the-problem-this-shape-solves) ·
[five beats, four planes](#five-beats-four-planes) ·
[the seams](#the-seams-producer-owned-boundary-objects) ·
[one signal, all the way through](#one-signal-all-the-way-through) ·
[where the code lives](#where-the-code-lives) ·
[deployment](#deployment-and-transport) ·
[observability](#two-observability-layers-never-fused) ·
[not built](#what-is-deliberately-not-built) ·
[provenance](#provenance)

---

## The problem this shape solves

The naïve version of "AI for SRE" is a straight line: an alert fires, a model
reads it, the model runs a command. That line has a name in this project —
**the Signal/Execution collapse** — and every structural decision here exists to
avoid it. It fails in three ways at once:

- **Nothing separates observation from interpretation.** The alert already says
  "system degraded," so the model reasons about somebody else's conclusion with
  no view of the evidence underneath it.
- **Nothing bounds what may happen.** The model's action space is whatever the
  shell can express. Blast radius is a function of the model's judgment, which is
  precisely the thing you cannot audit ahead of time.
- **Nothing holds policy.** "Don't do this during a freeze window" ends up as an
  `if` statement inside the reasoner, where it is invisible, untestable, and
  impossible to tighten without editing the thing that reasons.

thump answers each of those with a separation rather than a guardrail bolted on
afterward. Facts come from something that cannot interpret them; interpretation
from something that cannot act. Permission comes from something that cannot
re-reason; action from something that cannot decide. The rest of this document is
what those four seams look like in Go.

---

## Five beats, four planes

**Planes, not layers.** Layers stack and call downward; planes are orthogonal
concerns with their own failure containment, intersecting only through *boundary
objects*. Each plane has a single job and a **never**-clause — and the
never-clauses are the architecture. Delete them and what's left is an ordinary
automation pipeline with a language model in it.

| Beat | Plane | Single job | Never |
|------|-------|-----------|-------|
| **rattle** (Detect) | Signal | Represent reality | **Never interprets** — facts only |
| **clank** (Reason) | Reasoning | Structured truth → candidate actions | **Never acts** — proposals only |
| **hiss** (Govern) | Governance | Permission evaluation | **Never re-reasons** — verdicts only |
| **thump** (Act) | Execution | Approved actions → outcomes | **Never decides** — contracts only |
| **click** (Learn) | — *(return edge)* | Outcomes feed the next cycle | **Never a module** |

```
SignalDetection        ProposalSet             Decision                Outcome
      │                     │                       │                      │
  ┌───▼───┐   detects   ┌───▼───┐   selects     ┌───▼───┐   permits    ┌───▼───┐
  │rattle │────────────▶│ clank │──────────────▶│ hiss  │─────────────▶│ thump │
  └───────┘             └───────┘               └───────┘              └───┬───┘
   Signal                Reasoning              Governance                 │ acts
                                                                           ▼
                                                                    (the cluster)
                                                                           │
                                                     click reads Outcome back into
                                                     clank's case base — the return
                                                     edge, not a sixth box
```

Planes are conceptual separations, not necessarily separate processes. Today
it's one binary per beat in one Go module, which is fine **as long as the four
responsibilities stay structurally distinguishable**. The moment they mash
together the safety properties stop holding, and they stop holding *quietly*,
which is the dangerous part. That's why the never-clauses get their own
invariants and their own tests, rather than living in a style guide.

**Why click isn't a sixth box.** A "Learn service" has to reach across every
plane boundary to do its job — read signals, read proposals, read verdicts, read
outcomes — and in doing so it quietly reassembles the monolith you just
decomposed. click is thump's `Outcome` flowing back into clank's case base and
calibration numbers: wiring, not a binary. It lives in `internal/clank/click.go`
(`Click.Absorb`, `ReturnEdge`) and `internal/clank/metrics.go`'s `Recorder`.

Diagrams — C4 context/container/component and a sequence walk — are in
`c4-architecture.md`.

---

## The seams: producer-owned boundary objects

The boundary objects are the real design surface. The planes are whatever code
happens to sit on either side of them. **One producer per object**; consumers
read it and never reach into the producer's internals.

Every one of them lives in `api/v1/`, and every one of them is a **leaf
package** — `api/v1/signal` imports nothing but `time`. That's deliberate and
mostly pinned by a `leaf_test.go` walking the import graph (`internal/leaftest`):
the shared vocabulary can never grow an import into a beat's internals, so no
beat can accidentally depend on another beat's implementation by way of the
contract they share.

| Boundary object | Go type | Producer → Consumer |
|---|---|---|
| Signal Contract | `signal.Detection` | rattle → clank |
| Candidate Action | `proposal.Set` | clank → hiss |
| Governance verdict | `decision.Decision`, wrapped in `decision.Governed` | hiss → thump |
| Action Contract | `contract.ActionContract` (the catalog) | **humans author** → clank proposes from, thump executes from |
| Outcome Signal | `outcome.Outcome` | thump → click's return edge |
| Approval | `approval.Approval` | operator (`trim`) → hiss re-issues |

`api/v1` is **additive-only**: never rename, retype, or repurpose a field there.
Other *processes*, not just other packages, depend on the exact shape.

**Born-auditable.** `decision.Decision` and `outcome.Outcome` each carry an
`Auditable() error` method, and emitting one that fails it is a bug, not a
warning. A verdict with no policy version can't be re-checked later. A failure
result with no error text is silence dressed as a record. Non-approval is an
audit record, never absence — a rejected or held proposal lands in the trail with
its reasons attached.

**The audit thread.** One identifier runs the whole way through:

```
Detection.Fingerprint → Set.SignalRef → Decision.SignalRef → Outcome.SignalRef
```

That thread *is* the audit trail. Nothing in this system needs a second source of
truth to answer "why did it do that." It's also the dedup key
([I-14](invariants.md#i-14--delivery-is-at-least-once-identity-is-the-fingerprint)):
consumers dedupe on the producer-assigned fingerprint, never on transport
metadata like a filename or a stream sequence number.

---

## One signal, all the way through

This is the mechanism-level walk. Each stage names the file it lives in.

### 1 · rattle detects — and refuses to interpret

rattle polls an operator-authored watch list (`config/<site>/rattle/watch.yaml`)
of SLOs. Detectors — burn-rate acceleration, historical envelope (a trailing mean
± Kσ window) — compare observed against baseline and emit a `signal.Detection`
carrying:

- a **fingerprint**, assigned once, which everything downstream threads through
  and nothing downstream ever recomputes;
- a `Divergence` with `Observed`, `Baseline`, and `Confidence` — rattle's
  **signal-strength** number, answering *is this input trustworthy?*;
- `Impact`, split into two axes that are never collapsed into one badness score:
  `Severity` (how far off the objective) and `BlastRadius` (how broadly exposed);
- a topology read of the immediate neighbourhood.

Two things rattle does *not* do. It does not editorialize — there is no field on
`Detection` where "the cluster is unhealthy" could be written. And when a
declared dependency is degraded, it **attenuates rather than suppresses**:
confidence drops, the signal still ships. A silently dropped signal is
indistinguishable from a healthy system, which is the failure mode this whole
project is organized against.

`internal/rattle/{detector,envelope,watch,enrich,correlation}.go`

### 2 · clank reasons — bounded, digest-only, catalog-locked

`Engine.Propose` (`internal/clank/engine.go`) takes one `Detection` and returns
one `proposal.Set`. It never touches infrastructure; it reads evidence and writes
to a checkpoint store, a ledger, and a publisher. That's the entire blast radius
of the reasoning beat.

**Intake assembles the SAO** (`internal/clank/intake.go`). The Situational
Awareness Object is a versioned, frozen snapshot: the signal, the dependency
topology, and recent change events. It's assembled *once*, before the loop
starts, and never re-derived mid-loop. Everything the reasoner concludes is
traceable to a snapshot you can read back.

**The loop is bounded.** At most `MaxSteps` turns. Each turn the model may call
read-only tools — `metrics` (Prometheus), `loki`, `kube`, `casebase` — or end the
run by calling `propose` or `insufficient`. A run that burns through `MaxSteps`
without calling either is recorded as `budget_exhausted`; it is not an error and
it is not a silent nothing. Every turn is checkpointed before the next one runs,
and a checkpoint failure halts the run rather than risk an unrecorded turn. Re-running
is always safe, because nothing in the loop mutates anything.

**Digests only, never raw.** A tool call returns an `EvidenceRef`: the query, a
one-line `Summary`, a `Ref` pointer to re-fetch the source, and a `Live` bool.
There is no `Raw` field on `EvidenceRef` and there never will be. Raw payloads
cannot enter the conversation history — which bounds context, bounds cost, and
means the audit trail records a claim you can independently re-check rather than
a blob you have to trust.

**The catalog is enforced, not suggested** (`enforceCatalog`). Every proposed
`ContractRef` must name an action the catalog lists. Two distinct failure modes,
handled differently on purpose:

- a ref naming **no catalogued action at all** is `contract.ErrOutsideCatalog` and
  fails the run outright — this is where the autonomy boundary is enforced;
- a ref naming a **real action that doesn't apply to the failure class the model
  itself declared** is `errClassMismatch`, which becomes an auditable `no_action`
  decline. The model does not get an "I don't know" escape hatch that quietly
  resolves to *do something anyway*.

A candidate citing evidence the run never gathered is `errUngroundedCitation` —
also a decline, because it's a checkable mistake rather than an escape.

**Confidence is computed** (`internal/clank/confidence.go`). Each
candidate's emitted confidence is a product of what the run actually grounded:

```
computed = signalConfidence
         × groundingTier(live, in-topology citations: 0 / 1 / 2+)
         × (0.5 + 0.5 × caseBaseAlignment)   -- only if the case base cleared its own ≥2-vote floor
         × maxCausalLikelihood               -- only if there were change events to score

confidence = min(computed, modelSelfReportedConfidence)
```

Two properties are load-bearing. A term whose precondition fails **drops out of
the product entirely** rather than multiplying in as zero — absence is not
evidence of nothing. And the model's own stated confidence is applied as a
**ceiling**, never a floor: a confident-sounding guess with nothing behind it can
only be pulled down.

**The gate is a conjunction of minimums** (`internal/clank/gate.go`).
`budget ∧ dedup ∧ evidence`, never a weighted sum:

- `BudgetOK` — the real budget is `MaxSteps`, already spent by the time the gate runs.
- `DedupeOK` — false when an open set already exists for this fingerprint. Suppressed
  means *recorded*, not delivered.
- `EvidenceOK` — false unless at least one of the recommended candidate's
  citations resolves to an `EvidenceRef` that is both `Live` and **topologically
  coherent**: it names no subject at all, or names the affected service, or names
  a node in the frozen topology snapshot. A live metric about a service the
  signal has no declared relationship to may corroborate, but it can't clear the
  gate alone.

The set is recorded to the ledger whether or not the gate passes. It is only
*published* when it passes. And the whole ranked set travels, not just the
winner — "why X?" answers as "considered N actions, ranked them thus," never as
a bare choice.

### 3 · hiss governs — two stages, one question

`Authority.Evaluate` (`internal/hiss/authority.go`) is a pure function of
`(Set, Policy, now)`. Same inputs, same verdict, nothing persisted between calls.
It never mutates or re-ranks the set. It answers exactly one question: *allowed,
right now?*

**Stage 0 — standing.** A set whose gate didn't pass, or whose `Recommended` ID
doesn't resolve to a candidate in `Proposals`, is `rejected` with
`ungated_input`. That's an evidence gap upstream, not something hiss has standing
to weigh in on.

**Stage 1 — the minimums.** Four independent vetoes, any of which escalates:

| Reason | Fires when |
|---|---|
| `confidence_floor` | confidence is below the authored floor for this (tier, failure class) |
| `authority_ceiling` | the *requested* band outranks `maxBand` for the tier |
| `irreversible` | policy requires reversal and the candidate has no reversal path |
| `freeze_window` | now falls inside a declared freeze window |

Escalating is hiss asking for a human, not overruling clank. Two absence rules
matter here: a candidate carrying no governance level requests `observe`, the
*lowest* band — absence is never read as privilege — and a band value the ranker
doesn't recognize sorts *above* every real band, so an unparseable value fails
the ceiling instead of passing by default. Fail closed, in both directions.

**Stage 2 — the shaper.** Only reached once every minimum is met. It asks a
different question: not *is this eligible* but *how much latitude does it get
unattended?* `RiskBand` (`internal/hiss/risk.go`) is computed from authored facts
only — reversibility and blast tier — never from anything the model produced:

|  | blast `low`/`med` | blast `high` |
|---|---|---|
| **has a reversal path** | `act_reversible` | `act_disruptive` |
| **no reversal path** | `act_disruptive` | `act_disruptive` |

If that computed band outranks `autoBand` for the tier, the verdict is **`hold`**
with `risk_ceiling`: approved in principle, waiting on a human. Otherwise
`approved`, granting exactly the band requested — hiss grants what was asked or
it doesn't grant at all.

The gate and the shaper never blend
([I-5](invariants.md#i-5--gate--shaper)). A high score on one axis cannot buy
passage on a failed minimum.

**The hold loop closes.** An operator runs `trim approve <fingerprint>`, which
emits an `approval.Approval` onto `thump.approvals` and nothing else. hiss's
`approveHandler` (`internal/hiss/transport.go`) takes the held decision,
re-stamps it approved with the approver's name, and publishes it. Governance
still happens exactly once, in hiss. The operator surface never wrote a decision.

### 4 · thump acts — render, gate, execute, watch, undo

**Render** (`internal/thump/actuator.go`). The granted `Governed` becomes an
`Order`: the concrete mutation, the success criteria, the window, and the
reversal plan. Catalog-authored facts are carried onto the order *unconditionally*
— notably `Reversal.RestoreOnSuccess` and `Reversal.Fallback`, which come from the
catalog and must not be lost because the model happened not to propose a reversal
path.

**Mode.** `THUMP_EXECUTOR` is `dry` unless explicitly set to `live`. In dry mode
the order is rendered and nothing is touched; the outcome is
`{mode: dry_run, result: rendered}`. Rehearsal and reality are distinguishable in
the record, never inferred from the result alone.

**The kill switch** (`internal/thump/killswitch.go`, `gate.go`). One coarse,
global trust state for the whole execution subsystem. It only ever *subtracts*
authority hiss already granted — a circuit breaker, not a second governor. Three
details are the design:

- **Any doubt reads as disarmed.** A missing file, an unreadable one, malformed
  contents — all leave live actuation off. Only a well-formed file explicitly
  saying `armed: true` turns it on, and a failed reload *clears* a stale armed
  state rather than latching it.
- **A refusal is recorded, not skipped.** A blocked order emits
  `{mode: live, result: blocked}`. A blocked run is loud.
- **Reversals are exempt, on purpose.** `GatedExecutor` blocks forward orders
  only. Blocking cleanup mid-flight strands infrastructure half-changed, which is
  worse than letting one bounded, already-approved undo finish.

**Execute** (`internal/actuate`). The actuator compiles a closed set of
mechanisms — `scale`, `restart`, `flagVariant`, `exec` — and binds each
catalogued action's authored `execution` block to one of them at **startup**. An
unknown verb, a missing target, or a forward step with no reverse refuses to
load: the process fails to start and names the contract and the step. A
multi-step forward runs in authored order and stops at the first failing step.
`internal/actuate` is the only package in the tree that imports client-go, and a
structural test enforces that.

**Watch and settle** (`internal/thump/reversal.go`, `transport.go`). After the
authored success window elapses, one probe read decides everything, returning a
`Settlement`:

```go
type Settlement struct {
    Converged bool     // success vs partial_non_converging
    Fire      bool     // whether the undo runs at all
    Undo      Order    // the reversal order, zero when Fire is false
    Severity  *float64 // nil stays nil — unmeasured never becomes a fabricated 0.0
}
```

`Converged` and `Fire` are separate fields because the undo has **two** triggers,
and collapsing them into one bit would record a win as a failure:

- window **unmet** → reverse, and settle `partial_non_converging`;
- window **met**, and the contract authored `restoreOnSuccess: true` → *restore*
  (put the authored defaults back), and settle **`success`**. A temporary tuning
  knob that succeeded must not stay applied forever with nothing watching it.
- window met, no restore authored → do nothing further. The flag flip *was* the
  remediation.

The undo rides the original approval — no fresh governance pass — because the
reversal method was part of the action contract hiss already granted. It is the
second half of one governed transaction, not a new one.

### 5 · click absorbs

thump's `Outcome` flows back into clank's case base (`Click.Absorb`) and the
calibration metrics (`Recorder`). Historical alignment is one of the terms in the
confidence product above — discounted by freshness decay, and unable to raise
confidence on its own without live corroboration. The loop closes in `click.go`
and `metrics.go` — 325 lines of wiring, no sixth service.

---

## Where the code lives

```
api/v1/          the boundary objects — leaf packages, additive-only
  signal/        Detection, Divergence, Severity, BlastRadius
  proposal/      Set, Candidate, EvidenceRef, GateResult, SAO
  decision/      Decision, Governed, Verdict, Band
  outcome/       Outcome, Mode, Result
  approval/      Approval

internal/
  rattle/        detectors, envelopes, the watch list, enrichment, correlation
  clank/         the reason loop, intake/SAO, tools, causal scoring, confidence,
                 the ranker, the readiness gate, the case base, click's return edge
  hiss/          Authority.Evaluate, the risk shaper, policy, the decision ledger,
                 the held-decision store and the approval handler
  thump/         Actuator.Render, executors, the kill switch, the reversal watcher,
                 the settle path
  actuate/       the ONLY client-go site — compiled mechanisms and their binding
  contract/      the action catalog: loading, validation, reachability checks
  trim/          the operator CLI: read projection, approve, break-glass force
  whir/          topology and evidence-query config
  broker/        NATS JetStream subjects and durable consumers
  publish/       publishers, the WAL, S3 offload
  beat/          shared beat scaffolding: startup, stage metrics, tracing

cmd/             one main per binary: clank, rattle, hiss, thump, trim
config/          authored YAML — the catalog, failure classes, hiss policy,
                 and one directory per site
test/onboarding/ the whole engine over a domain authored in config alone
```

---

## Deployment and transport

The beats deploy as operators. Their outputs ride a NATS JetStream stream and
land in an S3-offloaded write-ahead log — **the log is the system of record.**
etcd holds slow, human-authored config only, and as little of that as possible;
there is no custom resource per noun. Any reporting view (`trim incidents`) is a
read-model, rebuilt by folding the stream, never a second source of record.

Subjects: `thump.detections`, `thump.proposals`, `thump.decisions`,
`thump.outcomes`, `thump.declines`, `thump.approvals`.

Delivery is **at-least-once**, and identity is the producer-assigned fingerprint.
Every consumer is idempotent and dedupes on that fingerprint — never on transport
metadata. "Exactly-once" is at-least-once composed with idempotent consumers;
this project implements the composition and declines the marketing term.

Each beat also runs in an **offline directory-poll mode** with no broker at all
(`CLANK_INBOX`/`CLANK_OUTBOX` and friends). That's not a toy path — it's how the
beats are tested end to end without infrastructure, and it's why the whole
pipeline is exercisable in `task ci`.

---

## Two observability layers, never fused

- **The audit trail** answers *why?* — the frozen SAO, the whole ranked proposal
  set, evidence refs, hypotheses and their weights, the verdict, its reasons, and
  the policy version it was reached under.
- **Telemetry** answers *how fast?* — structured `slog` JSON, rate/error/duration
  metrics per stage, and OpenTelemetry spans at each seam.

Keep them separate. Fusing them gets you dashboards that answer neither question:
a trace is not a rationale, and a rationale is not a latency budget.

The honesty rider runs through both. `Outcome.ObservedSeverity` is a `*float64`;
`nil` means *unmeasured* and renders as `unmeasured`, never as a `0` sitting next
to a real `0.60` looking like a clean win. Absence is a distinct, first-class
state everywhere in this system, including in what it shows you.

---

## What is deliberately not built

Named so it isn't mistaken for an oversight. Fuller reasoning in
`design-decisions.md`.

- **A risk shaper beyond reversibility and blast tier.** The computed band is
  deliberately narrow. A richer composite risk score is designed, not built.
- **A model-modulated magnitude multiplier.** The authored
  `severityReductionPct` baseline is stamped onto every candidate and measured
  against the observed reduction. The context-aware adjustment on top of it is
  still just the plan — today the number is copied verbatim from the catalog.
- **Signal validity checks in clank.** That's rattle's job, by construction.
- **Declarative preconditions.** The registry binding a precondition name in YAML
  to a Go predicate exists and is tested; no shipped action declares one, so no
  expression language was built. It gets one the day a real domain needs it.
- **Dynamic baselines.** rattle uses a flat trailing mean ± Kσ window.
- **Parallel decision-making.** One signal, one loop, one verdict.
- **A generic `patch` verb.** Declined with a reason — see
  `design-decisions.md`.

---

## Provenance

The four-plane architecture, the boundary-object discipline, the incident-response
loop, and the belief-formation defenses are David Jambor's, from *Agentic
Reliability Engineering* (O'Reilly), chapters 6–9. Build method comes from John
Arundel's *The Power of Go: Tools* and *The Power of Go: Tests*; delivery and
layout conventions from Joel Holmes's *Shipping Go*.

You don't need any of the three to read this repo or contribute to it.
Where a rule here comes from one of them, `invariants.md` says so and says what
it constrains in *this* code. Where the project knowingly does something
different, `design-decisions.md` says that too, with the reasoning. The rule the
project holds itself to is: **a divergence is fine when it's written down; drift
is a divergence that isn't.**
