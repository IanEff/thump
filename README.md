# thump

thump is a general-purpose, DRAL-based agentic SRE for a Kubernetes cluster —
a multidimensional thermostat that watches reliability signals, reasons about
them with an LLM, and executes an authored, catalog-bound action when policy
clears. What it can act on is entirely a function of what's in the catalog;
today that's a handful of rook/Ceph runbooks (RGW saturation, degraded
redundancy, PG rebalance storms), because a Ceph cluster is the rig on hand
to build and chaos-test against. Grow the catalog, grow what thump can act
on — the reasoner and the governor don't change.

It's also deliberately dumb, anal, and rigid. It cannot invent an action
outside the catalog, cannot invent a magnitude an action author didn't
authorize, cannot skip the gate because a hunch feels strong. That rigidity
is the whole safety argument, not a limitation apologized for — the rest of
this README is mostly about the shape of it: a fixed catalog of actions, a
governance pass that's structurally incapable of re-reasoning, a kill switch,
and a habit of declining out loud instead of guessing quietly.

If you want the load-bearing detail behind any of this — the full invariant
list, the conscious divergences from the book it's built from, the exact
file:line for every claim below — that's `docs/decision-engine-scope.md`'s
job eventually and the vault's job today; see [Source of truth](#source-of-truth).

---

## Table of contents

- [Authority model & guardrails](#authority-model--guardrails)
- [The five beats](#the-five-beats)
- [A golden path, worked end to end](#a-golden-path-worked-end-to-end)
- [Standing it up locally](#standing-it-up-locally)
- [Invariants (read as law)](#invariants-read-as-law)
- [Known-open](#known-open)
- [Building & testing](#building--testing)
- [Contributing](#contributing)
- [Source of truth](#source-of-truth)

---

## Authority model & guardrails

**The model proposes magnitude, it never invents it.** Every catalogued action
carries an authored `SeverityReductionPct` — in today's rook/Ceph catalog,
`throttle-non-critical-paths` is authored at 0.5, `scale-out-rgw-gateways` at
0.6, `accelerate-recovery` at 0.8 (`internal/contract/authored.go`). Whatever
runbook gets added next authors its own number the same way. The LLM picks
which action and how confident it is in the diagnosis; it does not get to
decide that *this* incident's throttle will cut severity by 73%. When an
action has no authored number (`hold-rebalance` doesn't), the forecast comes
back `nil`, not `0` — an unforecast action must never look like a forecast of
no effect.

**Modulates, never replaces.** The authored number above is a *prior*. The
design (in progress, not finished — see [Known-open](#known-open)) is for a
future SAO/topology-aware multiplier to adjust that prior up or down, never
substitute a model-vibed number for it. Half of this is built today — the
baseline is stamped onto every candidate — the multiplier itself isn't yet.

**Blast tiers, a kill switch, and a dedupe window bound what can go wrong.**
Every action carries a `BlastTier` (`low` / `med` / `high`) authored by a
human, not computed by the reasoner — `accelerate-recovery` is the one
`high`-tier action in the catalog today, because trading client I/O for
durability is a call a human should bless, not the loop. `hiss` reads the tier
against a policy (`config/hiss/policy.yaml`) and holds anything past the
auto-fire ceiling for a human. Underneath all of that sits one coarse,
disarm-anything kill switch (`THUMP_KILLSWITCH`, `internal/thump/killswitch.go`)
— live execution refuses to run at all while it's off, full stop, no partial
credit. And a `DedupeWindow` (default 1h, `DEDUPE_WINDOW`) stops a still-firing
signal from re-triggering a fresh action on top of one already in flight.

**Declining is a first-class outcome, not a failure to act.** `no_action` with
a cited reason (`ProposalSet.Status.Reason`) is a pass condition. It fires when
the model can't gather enough evidence, when a proposed action doesn't
actually apply to the failure class it claimed (`errClassMismatch` — the model
doesn't get an "I don't know" escape hatch that quietly maps to *do something
anyway*), or when the readiness gate vetoes on a single weak dimension. Silence
is the failure mode this project is built to not have.

**Zero doesn't mean "we expect zero effect."** `Outcome.ObservedSeverity` is a
`*float64` — nil means *unmeasured*, and it's rendered as `unmeasured`, never
as a `0` sitting next to a real `0.60` looking like a clean win. Every
honesty-rider field in this system follows the same rule: absence is a
distinct, first-class state, not a fallback value.

---

## The five beats

```
SignalDetection        ProposalSet             Decision                Outcome
      │                     │                       │                      │
  ┌───▼───┐   detects   ┌───▼───┐   selects     ┌───▼───┐   permits    ┌───▼───┐
  │rattle │────────────▶│ clank │──────────────▶│ hiss  │─────────────▶│ thump │
  └───────┘             └───────┘                └───────┘             └───┬───┘
   Signal                Reasoning               Governance                │ acts
                                                                            ▼
                                                                     (the cluster)
                                                                            │
                                                                     click reads Outcome
                                                                     back into clank's
                                                                     case base — the
                                                                     return edge, not
                                                                     a sixth box
```

| Beat | Plane | Job | Never |
|---|---|---|---|
| `rattle` | Signal | Detects reliability divergences, emits a fingerprinted `SignalDetection` | Never interprets — facts only |
| `clank` | Reasoning | Assembles an evidence snapshot (the SAO), investigates with read-only tools, proposes a ranked, confidence-scored `ProposalSet` | Never acts — proposals only |
| `hiss` | Governance | Evaluates a `ProposalSet` against policy — confidence floors, blast-tier ceilings, freeze windows — emits one `Decision` | Never re-reasons — verdicts only |
| `thump` | Execution | Renders (dry-run) or executes (live) an approved `Decision`, watches for convergence, auto-reverses on a missed success window | Never decides — contracts only |
| `click` | Learning | Feeds `Outcome`s back into clank's case base and calibration | Never a module — it's wiring, not a binary |

Three lines clank never crosses, because they're the whole safety argument:
it doesn't detect (that's rattle's signal, trusted read-only, fingerprint and
all); it doesn't execute (its entire output is a document); it doesn't
authorize (each candidate carries a *requested* governance band — a request,
never a verdict — and hiss is the only thing that converts a request into
allow/hold/deny).

---

## A golden path, worked end to end

The engine is general-purpose; the worked example below is just whatever
happens to be in the catalog today. This is a real fixture
(`internal/clank/testdata/golden/rgw-saturation-*.yaml`), not a hypothetical.

1. **rattle detects.** RGW latency and request-rate diverge from baseline —
   `severity.DegradationPct: 0.2`, trajectory `accelerating`. rattle fingerprints
   it `slo_burn:ceph-rgw` and hands off a `SignalDetection`. clank never
   recomputes this — it trusts the fingerprint and the confidence rattle
   assigned.
2. **clank reasons.** It assembles the SAO (the signal snapshot above, plus
   topology — `cephobjectstore`/`rook-operator` both `healthy`), calls the
   `metrics` tool twice for `rgw_get_put_latency_ms` and `rgw_request_rate`
   (both come back live, both get cited), forms the hypothesis
   `rgw_backend_saturation` at weight 0.85, and proposes
   `throttle-non-critical-paths` at confidence 0.85 — `blastTier: med`,
   `predictedImpact.severityReductionPct: 0.5` (the authored baseline, not a
   model guess), a `reversalPath` (`unthrottle`, triggered on `p99 < 250ms`).
3. **The gate passes.** `budgetOK`, `dedupeOK`, `evidenceOK` are all true — two
   live citations clear the forced-live-telemetry defense; the set was never
   at risk of getting through on historical alignment alone.
4. **hiss governs.** Policy's `tier-1` floor for `dependency_saturation` is
   0.75; the proposal's confidence (0.85) clears it. The action is reversible
   (`unthrottle` exists) at `BlastMed`, so hiss's shaper computes
   `RiskBand: act_reversible` — under `tier-1`'s `autoBand` ceiling, so this
   doesn't hold for a human. `Decision.Verdict: approved`,
   `grantedBand: act_reversible`, `floorApplied: 0.75` stamped onto the audit
   record.
5. **thump acts.** In dry-run mode (the default — see below) it renders the
   order and stops: `Outcome{mode: dry_run, result: rendered}`. In live mode,
   the same `Decision` would actually throttle anonymous RGW requests, then
   watch `latency_p99` against the 10-minute success window and auto-reverse
   through `unthrottle` if it doesn't converge.

Every step above is one JSON/YAML object with the same `signalRef` threaded
through it. That thread — `Detection.Fingerprint` →
`ProposalSet.SignalRef` → `Decision.SignalRef` → `Outcome.SignalRef` — is the
whole audit trail; nothing in this system needs a second source of truth to
answer "why did it do that."

---

## Standing it up locally

thump runs against three cluster profiles today (`Tiltfile`'s `CLUSTERS` dict):
`ceph-lab` (default), `rook-gke`, `rook-gce-k3s` — all rook/Ceph clusters,
because that's the rig this repo builds and chaos-tests against, not because
thump requires one. Bring one up, then:

```sh
tilt up -- --cluster=rook-gce-k3s   # or ceph-lab, rook-gke
```

**Dry-run is the default, and you have to opt into anything else.**
`THUMP_EXECUTOR` is `dry` unless you explicitly set it to `live`
(`internal/config/config.go`) — in dry mode, thump renders every approved
decision and touches nothing. Going live additionally requires an armed
`THUMP_KILLSWITCH` file; a disarmed switch reports `ResultBlocked` rather than
silently no-op'ing, so a blocked run is loud, not invisible. `SLACK_WEBHOOK_URL`
is optional — leave it unset and thump just doesn't page anyone on a hold or a
settle.

Check `internal/config/config.go` for the full environment variable list
before arming anything for real.

---

## Invariants (read as law)

These are excerpted from the vault's `thump-charter.md`, which is the
canonical, dated ledger — read it directly for the full text, sourcing, and
the divergence log (§5) tracking every place we knowingly depart from the
book this is built against. Numbered so a review can cite one directly
("this violates I-4").

1. **Signals describe state, never interpretation.** "p99 412ms vs 38ms
   baseline" is a signal; "system degraded" is a reasoning output. rattle
   never editorializes.
2. **Two confidence numbers, never one field.** Signal-strength confidence
   (is this input trustworthy?) is rattle's; hypothesis confidence (how sure
   is this diagnosis?) is clank's, computed from the first plus corroboration
   — not vibed.
3. **Policy lives only in Governance.** If clank grows an
   `if confidence < 0.8`, policy has become invisible and unauditable. hiss is
   the only policy holder.
4. **The catalog is the autonomy boundary.** Blast radius is bounded by a
   declared action's scope and reversal, never by the reasoner's judgment. A
   candidate outside the catalog is a hard error, not a soft ignore.
5. **Gate ≠ shaper.** The readiness gate is a strict conjunction of minimums
   — `budget ∧ dedup ∧ evidence` — never a weighted sum. A high score on one
   axis cannot buy passage on a failed minimum.
6. **The five belief-formation defenses are not optional.** A ≥2-source
   corroboration floor, freshness-decay on historical alignment, a
   predicted-but-absent signal that decrements rather than staying silent,
   a representable "partially fixed, still diverging" outcome, and a
   forced-live-citation rule on the gate. Together they're the defense
   against a cheap wrong belief compounding through scoring and memory.
7. **Reasoning selects, Governance permits.** hiss answers exactly one
   question — allowed, right now? — and never re-ranks or substitutes clank's
   recommendation.
8. **Learn is a return edge, not a module.** click is thump's `Outcome`
   flowing back into clank's case base — wiring, not a sixth binary with its
   own boundary-crossing reach.
9. **The signal contract owns the `if`.** Freshness bounds, confidence floors,
   exclusion windows — all live in rattle's contract, even when the transport
   is a poll ticker. Degraded trust attenuates confidence; it never silently
   drops the signal.
10. **Nothing executes ungoverned.** Every act is gated by hiss *and* the
    global kill switch, defaults to dry, and carries an executed reversal
    path. Highest blast radius gets the most paranoid on-ramp.
11. **The log is the system of record.** Detections and proposals ride the
    stream into an S3-offloaded WAL. Etcd holds slow, human-authored config
    only — no CRD-per-noun.
12. **The Trust Ceiling.** No autonomous write authority until real runtime
    Governance, action contracts with automatic reversal, signal contracts
    with declared guarantees, and calibrated confidence are *all four*
    simultaneously operational. Three of four doesn't count.
13. **Every wave stays red→green.** No untested seam crosses into the next
    beat.
14. **Delivery is at-least-once; identity is the fingerprint.** Every
    transport may redeliver; every consumer dedupes on the producer-assigned
    fingerprint, never on transport metadata like a filename or sequence
    number.
15. **The operator surface is read-only or evidence-producing — it never
    disposes.** A human interface onto the engine may read emitted state or
    emit an ack event, and nothing else; it may never write a decision,
    execute an action, or touch the kill switch. The one declared exception
    is a break-glass "force" path: a human, never the automated surface,
    disposing in Governance's place — attributed, audited, and rendered
    visibly `forced`, still kill-switch-gated. (This is the newest invariant
    and the interface it governs — `squawk`/`trim` — is designed, not built;
    see [Known-open](#known-open).)

---

## Known-open

Told straight, because "decline out loud instead of guessing quietly" applies
to this project's own status page too, not just its runtime behavior:

- **The operator surface (`squawk`/`trim`) is designed, not built.** There's a
  full design for a read-only reporting CLI plus a governed-write approval
  path for anything hiss holds for a human — it's a real gap today (a held
  action currently just re-pages on every dedupe window with no way to ack
  it), but no code exists yet. See the vault's `operator-surface-design.md`.
- **The model-modulates-the-prior multiplier isn't built.** The authored
  `SeverityReductionPct` baseline is stamped and measured today; the
  SAO/topology-aware adjustment on top of it is still just the plan.
- **A chaos-mesh v2.8.3 bug blocks one class of live test.** `toda`, the
  IOChaos fault injector, panics on startup on every OSD we've tried it
  against — not a config mistake on our side, confirmed against upstream.
  Until that's fixed or worked around, one signal class (OSD I/O latency
  injected at the FUSE layer) can't be chaos-tested end to end.
- **thump's own `ServiceMonitor` gaps have bitten us before.** A missing
  scrape target made a fully-working pipeline look broken from the outside
  more than once. If a live run looks dead, check Prometheus targets before
  assuming the engine is.

---

## Building & testing

Build tooling is [go-task](https://taskfile.dev) (`Taskfile.yaml`) — run
`task --list-all` for the full set.

| Command | What it does |
|---|---|
| `task run:clank` / `run:rattle` / `run:hiss` / `run:thump` | Run one beat |
| `task build` | Build all four beats to `bin/` |
| `task ci` | Full local CI: fmt-check → vet → lint → vulncheck → chart-lint → race → build |
| `task test` / `task race` | Tests, with `-race` |
| `task coverage` | Coverage profile + total |
| `task vulncheck` | govulncheck over deps |
| `task eval` | The reasoner eval against the production catalog — key-gated, not part of `task ci` |
| `go test ./internal/clank -run TestGate -v` | Run a single test |
| `gotestdox ./...` | Read test names back as a spec |

`task ci` green is the definition of done — it's also GitHub's gate, so
passing tests locally isn't the same claim as a green `task ci`.

---

## Contributing

This is a learning project as much as a build — the author is using it to get
fluent in Go, and the working agreement reflects that:

- **The repo owner lands every commit.** Edits, tests, and `task ci` are fair
  game for anyone helping out (including an AI pairing partner); the commit
  itself is always the owner's to make.
- **TDD is a spine, held loosely.** Red→green is the default shape, not a
  ritual enforced on every change — sometimes a spike or a tangent comes
  first, and that's fine.
- **Respect the seams.** The "never" column in the beat table above is the
  design. A policy check inside clank, a raw payload riding in a conversation
  message, a recomputed fingerprint, a new noun that isn't in the vocabulary
  above — these are the regressions that matter most, more than any bug in
  business logic.

Go conventions, comment style, and testing standards live in `AGENTS.md` —
read it before touching any `.go` file.

---

## Source of truth

The canonical architecture, invariants, and build plan live in the Obsidian
vault, not here — this README summarizes; the vault is authoritative:

- `thump-charter.md` — the adherence contract: every invariant above, sourced
  and dated, plus the full conscious-divergences ledger.
- `thump-readme.md` — the anchor doc and doc map for the whole five-beat
  engine.
- `{clank,rattle,hiss}-architecture.md` — design of record for each beat.
- `{clank,rattle,hiss}-implementation-guide.md` — the test-first build
  walkthrough for each beat.
- `beat-roadmap.md` — build sequencing; what's open, what's next.
- `thump-running-notes.md` — the dated investigation journal: bugs found on
  real clusters, decisions made, gotchas worth not re-discovering.
- `operator-surface-design.md` — the `squawk`/`trim` design referenced in
  [Known-open](#known-open).

Sourced from *Agentic Reliability Engineering* (the four-plane architecture,
agent-driven incident response, agentic delivery pipelines, belief-formation
defenses), with build method from *The Power of Go: Tools* and *Tests*, and
delivery/layout conventions from *Shipping Go*.
