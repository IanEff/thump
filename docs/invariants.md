# thump — the invariants

> ⚠️ **Provisional.** Ported from the author's working design docs and lightly
> de-jargoned; still written for someone already holding the context, not yet
> rewritten for outside readers. Accurate, but terse and assumption-heavy in
> places. A public-facing rewrite is owed.
>
> The authoritative, dated version — with per-rule sourcing and the full
> conscious-divergences ledger recording every place this project knowingly
> departs from the book it's built from — lives in the author's working notes.
> Where the two disagree, those are newer. See `README.md` § Source of truth.

Read `architecture.md` first for the shape these rules constrain.

**These are numbered so a code review can cite one** — "this violates I-4" is a
complete review comment here. Each one names what a violation looks like in
*this* codebase, not in the abstract.

---

## I-1 · Signals describe state, never interpretation

"p99 412ms vs 38ms baseline" is a signal. "System degraded" is a reasoning
output. The moment rattle interprets, clank is reasoning about someone else's
conclusion with no view of the evidence behind it.

*Violation smell:* a string field on `Detection` that editorializes.

## I-2 · Two confidence numbers, never one field

Signal-contract confidence (*is this input trustworthy* — freshness,
significance, exclusions) is **rattle's**. Candidate-action confidence (*how sure
is this hypothesis* — computed from signal confidence, cross-signal
corroboration, and historical rate; **never vibed**) is **clank's**. The second
is a function of the first, and they live on different boundary objects.

## I-3 · Policy lives only in Governance — the seam that rots first

If clank grows `if confidence < 0.8 { don't propose }`, policy has become
invisible, unenforceable, and unauditable — you can no longer tighten autonomy
without editing the reasoner. hiss is the only policy holder. A threshold handed
*to* something as data is fine; a threshold buried in a loop is not.

## I-4 · The catalog is the autonomy boundary

Blast radius is bounded by a declared `ActionContract`'s scope, duration, and
reversal — **never** by the reasoner's judgment. The model proposes *from* the
catalog; anything outside it is a hard error, not a soft ignore. Execution cannot
run what isn't catalogued. That's the safety property, not a limitation.

## I-5 · Gate ≠ shaper

The readiness gate is a **strict conjunction of minimums** —
`budget ∧ dedup ∧ evidence` — never a weighted sum. Risk *shaping* (which band of
latitude an eligible action earns) is a separate concern that runs *alongside* the
minimums and never blends into them. Blending lets a high score on one axis buy
passage on a failed minimum.

## I-6 · The five belief-formation defenses are not optional

All five are green on disk and stay load-bearing. Untested, they're decorative:

1. **A ≥2-source floor** — an uncorroborated case-base match can't raise
   confidence on its own.
2. **Freshness decay** on historical alignment.
3. **A predicted-but-absent signal decrements** — silence doesn't.
4. **`partial_non_converging` is a representable outcome** — binary
   success/failure is itself the belief-formation trap.
5. **Forced live citation** — an evidence set that is historical-only, with no
   fresh telemetry, vetoes at the gate.

## I-7 · Reasoning selects, Governance permits — different verbs

What crosses the clank→hiss seam is a ranked recommendation carrying a
*requested* authority level. hiss answers exactly one question — *allowed, right
now?* — and never re-ranks or substitutes. (The source book's own example schema
leaks this seam by putting `requires_human_approval` in the reasoning output.
Don't copy the book's bug.)

## I-8 · Learn is the return edge, not a module

See `architecture.md`. A Learn *service* reaches across every plane boundary and
reassembles the monolith.

## I-9 · The signal contract owns the `if`

Significance conditions — freshness bound, observation window, confidence floor,
exclusion windows — live in the contract, even when the transport is a poll
ticker. If clank wakes up and decides what's significant, Reason is doing
Signal's job. The seam isn't the transport; it's who owns the threshold logic.

*Corollary:* **attenuate, don't suppress.** Degraded trust lowers confidence; it
never silently drops the signal.

## I-10 · Nothing executes ungoverned

thump comes *after* hiss in the build order by design. Its first version rendered
the concrete action and **stopped** — dry-run by construction. That seal was
deliberately broken once the governance path was real, so the rule is no longer
"nothing executes" but "nothing executes *ungoverned*": every act is gated by
hiss **and** a global kill switch, defaults to dry, and carries an executed
reversal. The highest blast radius in the machine gets the most paranoid on-ramp.

## I-11 · etcd never sees the data path

The log is the system of record; etcd holds slow, human-authored config only. No
custom resource per noun. (This is a deliberate divergence from the source book's
CRD-as-output design, recorded as such.)

## I-12 · The trust ceiling

No autonomous write authority until **all four** of these are simultaneously
operational: real runtime governance, action contracts with automatic reversal,
signal contracts with declared guarantees, and calibrated confidence. Three of
four is not enough, and prompt-level safety doesn't count as any of them.

All four have been operational since automatic reversal moved from
stamped-to-executed — a real unmet success window fired a real undo on a live
rig. The ceiling is cleared, not aspirational; the rule stands as the bar any
*future* increase in write authority has to re-clear.

## I-13 · Every wave stays red→green, and every commit is reviewed

No untested seam crosses into the next beat. **Agents and contributors may edit
and test; the repo owner reviews and lands every commit.** Nothing enters history
unread.

*(This invariant previously read "agents don't touch the repo." It was amended
because it confused authorship with review — review is the property that actually
protects the tree — and because, read literally, it forbade the outside
contributors this project invites.)*

## I-14 · Delivery is at-least-once; identity is the fingerprint

Every transport this machine has or will have may deliver a boundary object more
than once. Consumers are built accordingly: idempotent, deduplicating on the
producer-assigned fingerprint, **never** on transport metadata.

*Violation smell:* any consumer whose correctness needs seeing a message exactly
once, or a dedupe keyed on a filename or sequence number.

## I-15 · The operator surface is read-only or evidence-producing; it never disposes

A human interface onto the engine may **read** emitted state (through a derived
projection over the stream, never by recomputing) or **emit an ack event**, and
nothing else. It may never write a decision, execute an action, or arm/disarm the
kill switch.

A held action resumes by *hiss re-issuing* an approved decision once a human ack
satisfies the condition hiss already attached — so governance still happens
exactly once, in hiss, and execution still never happens ungoverned.

The **sole declared exception** is a break-glass force path: a human — never the
automated surface — disposing in hiss's place. It is attributed, audited,
rendered visibly `forced` in every view, and still kill-switch-gated. Force
overrides *governance*, never the physical stop.

*Violation smell:* any interface code path reaching an executor, the kill-switch
file, or the decisions subject (force excepted, and even force never reaches the
kill switch); or a reporting view computing a verdict or severity the beats never
emitted.

---

## The smell test

Run this at every design review and wave boundary. Four questions; a "no" is a
finding, not a discussion:

1. Can every piece of state be pointed to **exactly one** boundary object?
2. Does anything bound blast radius **other than** a declared action-contract
   scope?
3. Does the reasoner read anything that **isn't** a contract-bound signal?
4. Is there any policy check living **anywhere but** Governance?

## On divergence

Adherence includes knowing where you deviate. This project keeps a ledger of
conscious divergences from the book it's built from, and the rule is:

> **A divergence is fine when it's in the table. Drift is a divergence that
> isn't.** Add the row *before* you diverge, not after someone notices.

That ledger lives in the author's working notes with dates and rationale. One
entry is worth knowing about if you touch the action catalog: the binding from a
catalogued action to a concrete cluster mutation is **data**, authored in
`config/actions/catalog.yaml`, and the `exec` verb takes argv. What bounds it is
RBAC, the kill switch, and hiss policy — not the verb list. See `CONTRIBUTING.md`.
