# thump — the invariants

Fifteen rules the engine is built to hold. They're numbered so a code review can
cite one: **"this violates I-4"** is a complete review comment here.

Read `architecture.md` first for the shape these rules constrain.

**Every rule below names three things** — where it comes from, what a violation
looks like in *this* codebase, and (where one exists) the test that would go red.
That third column is the point. An invariant nobody tests is decorative, and this
project would rather say "not enforced yet" out loud than let you assume it is.

**Sourcing.** Rules marked *(ARE ch-N)* come from *Agentic Reliability
Engineering*, the book this architecture is drawn from — chapter 6 is the
four-plane framework, 7 is agent-driven incident response, 8 is agentic delivery
pipelines, 9 is the belief-formation material. Rules marked *(ours)* are this
project's own, with the date they were adopted. You don't need the book; the rule
and its rationale are stated in full here.

Where the project knowingly does something the book doesn't, that's
`design-decisions.md`, not a silent exception.

---

## I-1 · Signals describe state, never interpretation

*(ARE ch-6)*

"p99 412ms against a 38ms baseline" is a signal. "System degraded" is a reasoning
output. The moment rattle interprets, clank is reasoning about someone else's
conclusion with no view of the evidence behind it — and the whole reason to have
a reasoning plane at all evaporates.

*Violation smell:* a string field on `signal.Detection` that editorializes.

## I-2 · Two confidence numbers, never one field

*(ARE ch-6)*

They answer different questions and belong to different beats.

- **Signal-strength confidence** — *is this input trustworthy?* Freshness,
  significance, exclusions. rattle's, on `signal.Divergence.Confidence`.
- **Hypothesis confidence** — *how sure is this diagnosis?* clank's, on
  `proposal.Candidate.Confidence`, computed from the first plus corroboration
  plus historical rate. **Never vibed.**

The second is a function of the first, and they live on different boundary
objects so they can never be quietly conflated.

*Enforced by:* `internal/clank/confidence.go` — the model's self-reported number
enters only as a `min()` ceiling. See the confidence formula in
`architecture.md`.

## I-3 · Policy lives only in Governance

*(ARE ch-6 — the seam that rots first)*

If clank grows `if confidence < 0.8 { don't propose }`, policy has become
invisible, unenforceable, and unauditable — you can no longer tighten autonomy
without editing the reasoner, and nothing records which rules a past decision was
made under. hiss is the only policy holder.

A threshold handed *to* something as data is fine. A threshold buried in a loop
is not.

*Violation smell:* a criticality tier, an error-budget check, or a confidence
floor appearing in `internal/clank` or `internal/rattle`. Note that
`ReadinessGate` carries **zero** policy on purpose — its evidence leg is a
belief-formation defense native to the reasoner, not a permission check.

## I-4 · The catalog is the autonomy boundary

*(ARE ch-6)*

Blast radius is bounded by a declared `ActionContract`'s scope, duration, and
reversal — **never** by the reasoner's judgment. The model proposes *from* the
catalog; anything outside it is a hard error, not a soft ignore. Execution cannot
run what isn't catalogued.

That's the safety property, not a limitation. It's also why the execution verb
set is closed: config *picks* a compiled mechanism and names its targets, it never
*describes* a new one.

*Enforced by:* `contract.ErrOutsideCatalog` in `internal/clank/engine.go`'s
`enforceCatalog` (a run that proposes off-catalog fails outright);
`TestShippedCatalog_EveryCatalogedActionIsActuatorBound`
(`internal/contract/binding_test.go`) and startup binding validation in
`internal/actuate` (an action naming no compiled mechanism refuses to load).

## I-5 · Gate ≠ shaper

*(ARE ch-8 §1b)*

The readiness gate is a **strict conjunction of minimums** —
`budget ∧ dedup ∧ evidence` — never a weighted sum. Risk *shaping* (how much
latitude an already-eligible action earns unattended) is a separate concern that
runs *alongside* the minimums and never blends into them.

Blending them lets a high score on one axis buy passage on a failed minimum,
which is exactly the property you cannot audit afterward.

*Enforced by:* `ReadinessGate.Evaluate` computes `budgetOK && dedupeOK &&
evidenceOK` with no arithmetic anywhere near it; `Authority.Evaluate` reaches
stage two only when stage one produced **zero** reasons.

## I-6 · The five belief-formation defenses are not optional

*(ARE ch-9 §7.7)*

Together they're the defense against a cheap wrong belief compounding through
scoring and memory. All five are green on disk; untested, they'd be decorative.

1. **A ≥2-source floor** — an uncorroborated case-base match can't raise
   confidence on its own.
2. **Freshness decay** on historical alignment — an old match counts for less.
3. **A predicted-but-absent signal decrements.** If a change event predicts
   indicators that aren't there, likelihood goes *down*. Silence doesn't get to
   be neutral.
4. **`partial_non_converging` is a representable outcome.** Binary
   success/failure is itself the belief-formation trap: a vocabulary that can't
   say "it half-worked and isn't settling" will round it to one of the two lies.
5. **Forced live citation** — an evidence set that is historical-only, with no
   fresh telemetry, vetoes at the gate.

*Enforced by:* `internal/clank/causal.go` (1–3), `outcome.ResultPartialNonConverging`
plus the settle path in `internal/thump/transport.go` (4), `gate.go`'s
`anyCoherentLive` (5).

## I-7 · Reasoning selects, Governance permits — different verbs

*(ARE ch-6)*

What crosses the clank→hiss seam is a ranked recommendation carrying a
**requested** authority level. hiss answers exactly one question — *allowed,
right now?* — and never re-ranks or substitutes a different candidate.

*(The source book's own example schema leaks this seam by putting
`requires_human_approval` in the reasoning output. Don't copy the book's bug —
that's why `proposal.GovernanceLevel`'s doc comment says "a request, not a
verdict.")*

*Enforced by:* `Authority.Evaluate` takes the set by value and returns a
`Decision`; `GrantedBand` is always either equal to `RequestedBand` or unset.
hiss grants what was asked or it doesn't grant at all.

## I-8 · Learn is the return edge, not a module

*(ARE ch-6 — an explicit warning in the source)*

A Learn *service* has to reach across every plane boundary to do its job, and
reassembles the monolith the planes just decomposed. click is thump's `Outcome`
flowing back into clank's case base and calibration numbers: wiring, not a
binary.

*Violation smell:* a `cmd/click`.

## I-9 · The signal contract owns the `if`

*(ARE ch-6)*

Significance conditions — freshness bound, observation window, confidence floor,
exclusion windows — live in rattle's contract, even when the transport is a plain
poll ticker. If clank wakes up and decides what's significant, Reason is doing
Signal's job. The seam isn't the transport; it's who owns the threshold logic.

*Corollary:* **attenuate, don't suppress.** Degraded trust lowers confidence; it
never silently drops the signal. A dropped signal is indistinguishable from a
healthy system.

## I-10 · Nothing executes ungoverned

*(ARE ch-6, sharpened by us 2026-07-15)*

thump comes *after* hiss in the build order by design. Its first version rendered
the concrete action and **stopped** — dry-run by construction. That seal was
deliberately broken once the governance path was real, so the rule is no longer
"nothing executes" but "nothing executes *ungoverned*":

- every act is gated by hiss **and** the global kill switch;
- the executor defaults to dry, and live is an explicit opt-in;
- every action carries an executed reversal path, not a stamped one.

The highest blast radius in the machine gets the most paranoid on-ramp.

*Enforced by:* `GatedExecutor` (`internal/thump/gate.go`); `FileSwitch`, which
fails **closed** on any read or parse error rather than latching a stale armed
state; `THUMP_EXECUTOR` defaulting to `dry` in `internal/config`.

*Known scope:* a reversal order is deliberately exempt from the kill switch —
blocking cleanup mid-flight strands infrastructure half-changed. That exemption
is bounded to an already-approved undo of an action hiss already granted, and it
never reaches the switch itself.

## I-11 · etcd never sees the data path

*(ours, 2026-07-01)*

Beats deploy as operators; their outputs ride the stream and land in the
S3-offloaded WAL. **The log is the system of record.** etcd holds slow,
human-authored config only, and as little of that as possible. No custom resource
per noun. Any reporting view is a read-model, rebuildable from the log, never a
second source of record.

*(This is a deliberate divergence from the book's CRD-as-output design — see
`design-decisions.md`, D-1.)*

## I-12 · The trust ceiling

*(ARE ch-6, the autonomy maturity model)*

No autonomous write authority until **all four** of these are simultaneously
operational:

1. real runtime governance,
2. action contracts with automatic reversal,
3. signal contracts with declared guarantees,
4. calibrated confidence.

Three of four is not enough, and prompt-level safety doesn't count as any of
them.

All four have been operational since automatic reversal moved from stamped to
*executed* — a real unmet success window fired a real undo on a live rig
(2026-07-16). The ceiling is cleared, not aspirational; the rule stands as the
bar any *future* increase in write authority has to re-clear.

## I-13 · Every wave stays red→green, and every commit is reviewed

*(ours, amended 2026-07-25)*

No untested seam crosses into the next beat. **Contributors and AI pairing
partners may edit and test; the repo owner reviews and lands every commit.**
Nothing enters history unread.

*(This previously read "agents don't touch the repo." It was amended because it
confused authorship with review — review is the property that actually protects
the tree — and because, read literally, it forbade the outside contributors this
project invites.)*

## I-14 · Delivery is at-least-once; identity is the fingerprint

*(ours, 2026-07-05)*

Every transport this machine has or will have — directories, JetStream, whatever
follows — may deliver a boundary object more than once. Consumers are built
accordingly: idempotent, deduplicating on the **producer-assigned fingerprint**,
never on transport metadata like a filename or a sequence number.

"Exactly-once" is at-least-once composed with idempotent consumers. This project
implements the composition and declines the marketing term.

*Violation smell:* any consumer whose correctness needs seeing a message exactly
once, or a dedupe keyed on transport metadata.

## I-15 · The operator surface never disposes

*(ours, 2026-07-17)*

A human interface onto the engine — the `trim` CLI, Slack, a dashboard, a future
web UI — may do exactly two things: **read** emitted state through a derived
projection over the stream (never by recomputing it), and **emit an ack event**.
Nothing else. It may never write a decision, execute an action, or arm/disarm the
kill switch.

A held action resumes by **hiss re-issuing** an approved decision once a human ack
satisfies the condition hiss already attached. So governance still happens exactly
once, in hiss (I-3, I-7), and execution still never happens ungoverned (I-10).

The **sole declared exception** is a break-glass force path (`trim force`): a
human — never the automated surface — disposing in hiss's place. It is
attributed, audited, rendered visibly `forced` in every view, and still
kill-switch-gated. **Force overrides governance, never the physical stop.**

*Violation smell:* any interface code path reaching an executor, the kill-switch
file, or the decisions subject (force excepted, and even force never reaches the
kill switch); or a reporting view computing a verdict or severity the beats never
emitted.

---

## The smell test

Run this at every design review and whenever a new field doesn't obviously belong
to one boundary object. Four questions; a "no" is a finding, not a discussion:

1. Can every piece of state be pointed to **exactly one** boundary object?
2. Does anything bound blast radius **other than** a declared action-contract
   scope?
3. Does the reasoner read anything that **isn't** a contract-bound signal?
4. Is there any policy check living **anywhere but** Governance?

---

## The guards you'll actually hit

The invariants above are enforced by ordinary tests, not by ceremony. These are
the ones most likely to go red on a change, and what each one is protecting:

| Test | Protects |
|---|---|
| `TestShippedCatalog_EveryContractIsWellFormed` | I-4 — a typo that unmarshals clean and leaves an action unreachable |
| `TestShippedCatalog_EveryCatalogedActionIsActuatorBound` | I-4 — a catalogued action with no compiled mechanism behind it |
| `TestShippedCatalog_RestoreOnSuccessMatchesEachActionsAuthoredIntent` | I-10 — a temporary change that would outlive its grant |
| `TestPolicy_FloorsCoverEveryActuatableClass` | I-3 — a (tier, class) pair with no confidence floor silently clears the veto |
| `TestThumpCannotReachInfrastructure` | I-10 — infra-reaching imports staying inside `internal/actuate` |
| `TestNotifierSDKNeverReachesCoreBeats` | plane containment — a notification SDK leaking into a beat |
| `leaf_test.go` (in `api/v1/*`) | the seams — a boundary object growing an import into a beat |
| `TestOperator_OnboardsANewDomainInConfigAlone` | the config-only claim — the day onboarding needs Go, this is where it surfaces |

---

## On divergence

Adherence includes knowing where you deviate. The rule:

> **A divergence is fine when it's written down. Drift is a divergence that
> isn't.** Add the entry *before* you diverge, not after someone notices.

`design-decisions.md` is that record — every place this project knowingly departs
from the book, with what it does instead and why.

One entry is worth knowing about before you touch the action catalog: the binding
from a catalogued action to a concrete cluster mutation is **data**, authored in
`config/actions/catalog.yaml`, and the `exec` verb takes argv. What bounds it is
RBAC, the kill switch, and hiss policy — **not** the verb list. A catalog PR is
an execution-surface PR. See `CONTRIBUTING.md`.
