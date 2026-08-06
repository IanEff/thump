# thump

[![CI](https://github.com/IanEff/thump/actions/workflows/ci.yml/badge.svg)](https://github.com/IanEff/thump/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/IanEff/thump?logo=go)](https://go.dev)
[![Go Reference](https://pkg.go.dev/badge/github.com/IanEff/thump.svg)](https://pkg.go.dev/github.com/IanEff/thump)
[![License](https://img.shields.io/github/license/IanEff/thump)](LICENSE)

![Thump](assets/rainbow_thump.png)

thump is a general-purpose agentic SRE engine for Kubernetes — a
multidimensional thermostat that watches reliability signals, reasons about them
with an LLM, and executes an authored, catalog-bound action once policy clears.

What it can act on is entirely a function of what's in the catalog. Today that's
a handful of rook/Ceph runbooks and two OpenTelemetry-demo remedies, because
those are the rigs I have on hand to build and chaos-test against. Grow the
catalog and you grow what thump can act on; the reasoner and the governor don't
change. [Onboarding a domain in config alone](#onboard-your-own-domain) is the
test of that.

It is also deliberately dumb, anal, and rigid. It cannot invent an action
outside the catalog. It cannot invent a magnitude the action's author didn't
authorize. It cannot skip the gate because a hunch feels strong, and it cannot
execute anything a separate governor permitted. The rigidity is the safety
argument, and most of this README is the shape of it: a fixed catalog of
actions, a governance pass that cannot re-reason, a kill switch that fails
closed, an undo that fires on success as well as failure, and a habit of
declining out loud.

## Provenance, stated up front

The four-plane architecture, the boundary-object discipline, the
incident-response loop, and the belief-formation defenses are David Jambor's,
from *Agentic Reliability Engineering* (O'Reilly). thump exists because the book
kept circling a shape it never built. It specifies confidence-gated authority
and a continuous learning loop, then leaves calibrated confidence as a tuning
detail rather than the load-bearing question the whole design rests on. Those
two unspecified mechanisms are what this repo actually is.

Build method comes from John Arundel's *The Power of Go: Tools* and *The Power
of Go: Tests*. Delivery and layout conventions come from Joel Holmes's
*Shipping Go*. You don't need any of the three to read this repo. Where a rule
traces to one of them, [`docs/invariants.md`](docs/invariants.md) says so and
says what it constrains in *this* code; where I knowingly departed from Jambor,
[`docs/design-decisions.md`](docs/design-decisions.md) is the ledger.

**If you're evaluating whether to point this at your own systems**, the two
sections that matter are [Authority model & guardrails](#authority-model--guardrails)
and [Onboard your own domain](#onboard-your-own-domain). For the load-bearing
detail — the beats and their seams, the full invariant list, the departures —
go to [`docs/`](#documentation).

---

## Table of contents

- [Why this shape](#why-this-shape)
- [Authority model & guardrails](#authority-model--guardrails)
- [The five beats](#the-five-beats)
- [Repo tour](#repo-tour)
- [A golden path, worked end to end](#a-golden-path-worked-end-to-end)
- [Onboard your own domain](#onboard-your-own-domain)
- [Install](#install)
- [Standing it up locally](#standing-it-up-locally)
- [Invariants (read as law)](#invariants-read-as-law)
- [Known-open](#known-open)
- [Building & testing](#building--testing)
- [Contributing](#contributing)
- [Documentation](#documentation)

---

## Why this shape

The obvious way to build AI for SRE is a straight line: alert fires, model reads
it, model runs a command. Every structural decision in this repo exists to avoid
that line, because it fails three ways at once.

Nothing separates observation from interpretation. The alert already says
"system degraded," so the model reasons about somebody else's conclusion with no
view of the evidence underneath it.

Nothing bounds what may happen. The action space is whatever a shell can
express, which makes blast radius a function of the model's judgment —
precisely the thing you cannot audit ahead of time.

Nothing holds policy. "Not during a freeze window" ends up as an `if` statement
inside the reasoner, where it's invisible, untestable, and impossible to tighten
without editing the thing that reasons.

thump answers each with a separation rather than a guardrail bolted on
afterward. Facts come from something that cannot interpret them; interpretation
from something that cannot act. Permission comes from something that cannot
re-reason; action from something that cannot decide. The rest of this document
is what those four seams look like in Go.

---

## Authority model & guardrails

**The model proposes magnitude; it never invents it.** Every catalogued action
carries an authored `severityReductionPct`. In today's catalog,
`throttle-non-critical-paths` sits at 0.7, `accelerate-recovery` at 0.8, and
`restart-cart-pod` at 0.1 (`config/actions/catalog.yaml`).

That last one is the interesting case. Restarting the cart pod is a real,
reversible, low-blast action that clears the readiness gate on its own merits,
and 0.1 is what discriminates it from the action that actually fixes the fault —
the fault is flagd flag state, not pod state. The LLM picks *which* action and
how confident it is in the diagnosis. It does not get to decide that this
incident's throttle will cut severity by 73%. An action authored with no number
at all forecasts `nil`, not `0`, because an unforecast action must never look
like a forecast of no effect.

**Confidence is computed.** A candidate's emitted confidence is the product of
what the run actually grounded: rattle's signal-strength number, times a tier
set by how many live, topologically coherent citations back the candidate, times
a case-base alignment term (only if the case base cleared its own two-vote
floor), times the strongest causal likelihood (only if there were change events
to score). The model's own stated confidence enters as a ceiling and nothing
else. A confident-sounding guess with nothing behind it can be pulled down. It
can never talk itself up.

**Blast tiers, a kill switch, and a dedupe window bound what can go wrong.**
Every action carries a human-authored `blastTier` (`low`/`med`/`high`) —
`accelerate-recovery` is the one `high` in today's catalog, because trading
client I/O for durability and pausing the storage operator to make it stick is a
call a human should bless. hiss reads reversibility and tier against
`config/hiss/policy.yaml` and **holds** anything past the auto-fire ceiling for
an ack.

Underneath that sits one coarse kill switch (`THUMP_KILLSWITCH`,
`internal/thump/killswitch.go`) that fails closed in every ambiguous case. A
missing file, an unreadable one, malformed contents — all leave live actuation
off, and a failed reload *clears* a previously armed state rather than latching
it. A blocked order records `{mode: live, result: blocked}`, so refusals are
loud. A `DedupeWindow` (default 1h, `DEDUPE_WINDOW`) stops a still-firing signal
from stacking a fresh action on top of one already in flight.

> One deliberate exemption: a **reversal** order is not blocked by a disarmed
> switch. Blocking cleanup mid-flight strands infrastructure half-changed, which
> is worse than letting one bounded, already-approved undo finish. Force
> overrides governance; nothing overrides the switch in the forward direction.

**The undo has two triggers.** A missed success window reverses, which is
ordinary. But an action whose contract declares `restoreOnSuccess: true` also
restores its authored defaults on a **met** window, and still settles the
outcome as `success`. That closes the case where a temporary tuning change
worked and then stayed applied forever with nothing watching it.

Whether an action is temporary is the author's declaration. The watcher doesn't
get to infer it. `disable-cart-failure` succeeding means *stay disabled* —
the flag flip is the remediation. `accelerate-recovery` succeeding means *put the
knobs back and unpause the operator*. Same convergence verdict, opposite correct
behavior.

**Declining is a first-class outcome.** `no_action` with a cited reason
(`ProposalSet.Status.Reason`) is a pass condition. It fires when the model can't
gather enough evidence; when a proposed action doesn't apply to the failure
class the model itself declared (`errClassMismatch`, so there's no "I don't
know" escape hatch that quietly maps to *do something anyway*); when a candidate
cites evidence the run never gathered; and when the readiness gate vetoes on a
single weak dimension. Silence is the failure mode this project is built not to
have.

**Zero doesn't mean "we expect zero effect."** `Outcome.ObservedSeverity` is a
`*float64`. Nil means unmeasured and renders as `unmeasured`, so it never sits
next to a real `0.60` looking like a clean win. Every honesty-rider field in the
system follows that rule: absence gets its own state instead of borrowing a
value that already means something else.

---

## The five beats

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

| Beat | Plane | Job | Never |
|---|---|---|---|
| `rattle` | Signal | Detects reliability divergences, emits a fingerprinted `SignalDetection` | Never interprets — facts only |
| `clank` | Reasoning | Assembles an evidence snapshot (the SAO), investigates with read-only tools, proposes a ranked, confidence-scored `ProposalSet` | Never acts — proposals only |
| `hiss` | Governance | Evaluates a `ProposalSet` against policy — confidence floors, blast-tier ceilings, freeze windows — emits one `Decision` | Never re-reasons — verdicts only |
| `thump` | Execution | Renders (dry-run) or executes (live) an approved `Decision`, watches for convergence, undoes on a missed window or an authored restore | Never decides — contracts only |
| `click` | Learning | Feeds `Outcome`s back into clank's case base and calibration | Never a module — it's wiring, not a binary |

Three lines clank never crosses, because together they are the safety argument.
It doesn't detect; rattle's signal is trusted read-only, fingerprint and all. It
doesn't execute; its entire output is a document. It doesn't authorize; each
candidate carries a *requested* governance band — a request, never a verdict —
and hiss is the only thing that converts a request into allow/hold/deny.

Mechanism-level detail for each beat is in
[`docs/architecture.md`](docs/architecture.md).

---

## Repo tour

The beat table above is the concept. This is where it physically lives.

| Beat | Entrypoint | Package | Open first |
|---|---|---|---|
| rattle | `cmd/rattle` | `internal/rattle` | `doc.go` |
| clank | `cmd/clank` | `internal/clank` | `doc.go` |
| hiss | `cmd/hiss` | `internal/hiss` | `hiss.go` |
| thump | `cmd/thump` | `internal/thump` | `thump.go` |
| click | none (see table above) | `internal/clank/click.go`, `metrics.go` | `Click.Absorb` and `ReturnEdge` in `click.go` |

`cmd/` also houses `bootstrap` (in-cluster setup job) and `calipers`, the operator CLI: `incidents`/`approve`/`force` (the hold→ack loop), `unseal` (vault unseal), `corpus` (corpus management), `rca` (graded RCA suite), `tune` (scoring weight sweep), `replay` (transcript replayer), and `harvest` (chaos scenario runner) all live behind one binary now, dispatched by verb — see `internal/calipers/calipers.go`. Investigation tools (`metrics`, `loki`, `kube`, `argocd`) live in `internal/evidence`, LLM provider clients in `internal/anthropic` and `internal/gemini`, and coordinate resolution in `internal/subjects`.

**If you're opening one file, open `internal/clank/doc.go`.** clank is the
reasoning plane — the seam with no prior art to copy from, and the one
carrying the belief-formation defenses ([`docs/invariants.md`](docs/invariants.md),
I-6). rattle and hiss are comparatively conventional: a detector and a policy
evaluator. clank is where "bounded" had to be engineered rather than assumed.

Test-support packages sit next to the beat they serve rather than under a
shared `testutil`: `natstest` (an embedded, restartable NATS server),
`s3test`, `configtest`, `leaftest` (the import-allowlist check that pins a
package's dependency leaves — `internal/tlsx/leaf_test.go` uses it to prove
`tlsx` imports nothing beyond `crypto/tls`, `crypto/x509`, `fmt`, `os`, and
`sync`), and `tlsxtest`.

**`internal/tlsx` + `internal/tlsxtest` are worth the detour.** `tlsx.go` is
the one place a `*tls.Config` gets built, for the same reason
`internal/httpx` centralizes outbound HTTP clients: a config assembled at the
call site is a chance to get the root pool, the minimum version, or
client-cert verification wrong, and every one of those mistakes succeeds at
runtime instead of failing. `tlsxtest.go` mints a throwaway ECDSA CA and
leaves from it per test rather than committing a PEM fixture that quietly
expires, which is what makes the negative cases assertable at all:
`TestClient_ServerLeafFromDifferentCA_HandshakeRefused`,
`TestClient_ExpiredServerLeaf_HandshakeRefused`,
`TestServer_ClientPresentsNoCertificate_HandshakeRefused`,
`TestServer_RotatedKeypair_PickedUpWithoutRestart`. None of them need a
cluster or a network. A negative case is the entire value of a TLS config,
and this is the pair of files that makes negative cases cheap to write.

---

## A golden path, worked end to end

The engine is general-purpose, so the example below deliberately uses a service
the engine has never heard of: `acme`, authored entirely in YAML. This isn't
hypothetical. It's
`test/onboarding/onboard_test.go`'s `TestOperator_OnboardsANewDomainInConfigAlone`,
which runs in `task ci` with no API key and no cluster. Read it back with
`go test ./test/onboarding -v`.

1. **rattle detects.** `acme_api_error_ratio` diverges from baseline — observed
   0.42 against a 0.001 baseline, `severity.DegradationPct: 0.42`, trajectory
   `accelerating`. rattle fingerprints it `fp-acme-api-availability-001` and
   hands off a `SignalDetection`. clank never recomputes this; it trusts the
   fingerprint and the 0.9 signal confidence rattle assigned.
2. **clank reasons.** It assembles the SAO: the signal snapshot, plus topology
   resolved from the authored dependency graph (`acme-db` and `acme-cache`, both
   `healthy` per their authored state queries). It calls the `metrics` tool for
   `acme_api_error_ratio` and `acme_db_connections_saturation`, cites both, and
   proposes `acme-shed-load` — `blastTier: low`, a reversal path
   (`restore-acme-capacity`), and `serving_replicas: 2`, the authored default
   from the action's own scope range rather than a number the model picked.
3. **The gate passes.** `budgetOK`, `dedupeOK`, `evidenceOK` all true. Two live,
   topologically coherent citations clear the forced-live-telemetry defense, so
   confidence computes to 0.9 (rattle's 0.9 × full corroboration grounding,
   capped by the model's own self-report). The set was never at risk of getting
   through on historical alignment alone.
4. **hiss governs.** The authored policy's `tier-1` floor for `service_failure`
   is 0.75, and 0.9 clears it. The action is reversible at `blastTier: low`, so
   the shaper computes `RiskBand: act_reversible` — inside `tier-1`'s `autoBand`
   ceiling, so it doesn't hold for a human. `Decision.Verdict: approved`,
   `policyVersion: acme-v1`, `floorApplied: 0.75` stamped onto the audit record.
5. **thump acts.** In dry-run mode, the default, it renders the order and stops:
   `Outcome{mode: dry_run, result: rendered}`. In live mode the same `Decision`
   would scale `acme-api` to its authored floor, watch `acme_api_error_ratio`
   against the 5-minute success window, and undo through the authored
   `execution.reverse` if it doesn't converge.

Declines are the more common shape, and they get captured too.
`internal/clank/testdata/detections/ceph-rgw-saturation.yaml` is a raw JetStream
capture off a live rig where the reasoner read healthy upstream and capacity
evidence, ruled out the classes that didn't fit, landed on `traffic_shift`, and
proposed **nothing**, because no catalog action maps there. hiss caught it
downstream as `ReasonUngatedInput` and thump declined to act. That fixture is
pinned as a decision boundary, not a regression.

Every step above is one JSON/YAML object with the same `signalRef` threaded
through it. That thread — `Detection.Fingerprint` → `ProposalSet.SignalRef` →
`Decision.SignalRef` → `Outcome.SignalRef` — is the entire audit trail. Nothing
here needs a second source of truth to answer "why did it do that."

---

## Onboard your own domain

**Onboarding a system to thump takes no Go.** Signals, evidence, topology,
failure-class meanings, the action catalog, and how each action executes are all
operator-authored YAML, and there's a test that proves it:
`test/onboarding/` onboards a synthetic `acme` service from seven files and
drives it through all five beats in `task ci`. Copy that directory as a starting
point.

| File | What it owns |
|---|---|
| `rattle/watch.yaml` | the SLOs to poll, and the dependencies whose health gates trusting a divergence |
| `whir/catalog-info.yaml` | the dependency graph (Backstage `catalog-info` shape) |
| `whir/state-queries.yaml` | per-dependency "is it healthy right now" PromQL |
| `whir/evidence-queries.yaml` | the read-only PromQL the reasoner may cite, and what each result is *about* |
| `actions/failure-classes.yaml` | what each failure class means for your domain |
| `actions/catalog.yaml` | the actions that may be proposed, and how each one executes |
| `hiss/policy.yaml` | confidence floors, blast-tier ceilings, freeze windows |

An action's `execution` block names a **bounded mechanism** the actuator
compiles and points it at your resources:

```yaml
- name: acme-shed-load
  applicableFailureClasses: [service_failure]
  applicableTiers: [tier-1]
  blastTier: low
  reversal: {method: restore-acme-capacity, fallback: page-oncall}
  execution:
    forward: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 2}]
    reverse: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 10}]
```

The verb set is closed — `scale`, `restart`, `flagVariant`, `exec` — and checked
at **startup** rather than on first approval. A verb with no compiled mechanism,
a missing target, or a forward step with no `reverse` refuses to load. Config
*picks* a mechanism and names its targets; it never *describes* a new one.

**The honest 5%.** A genuinely new kind of cluster mutation needs a new
mechanism in `internal/actuate` plus a test. That's the autonomy boundary
earning its keep — the bounded vocabulary is precisely why the catalog can be
trusted as the blast-radius bound. It's also a welcome contribution.

> ⚠️ **A catalog PR is an execution-surface PR.** The `exec` verb takes argv, so
> whoever can merge `config/actions/catalog.yaml` can run a command in any pod
> thump's ServiceAccount can reach. What bounds this is RBAC (`pods/exec`,
> scoped per namespace), the kill switch, and hiss's policy — *not* the verb
> list. Scope that ServiceAccount tightly and review catalog changes
> accordingly; see `CONTRIBUTING.md`.

Full walkthrough, including the environment each beat reads and the guards that
catch an authoring mistake: [`docs/onboarding.md`](docs/onboarding.md).

---

## Install

Tagged releases ship prebuilt archives for linux and darwin, amd64 and arm64,
bundling six binaries — the four long-running beats (`rattle`, `clank`, `hiss`,
`thump`) plus `bootstrap` and `calipers`, the operator CLI:

```sh
gh release download v0.1.0 --repo IanEff/thump --pattern '*_linux_x86_64.tar.gz'
tar xzf thump_0.1.0_linux_x86_64.tar.gz
```

Checksums are published alongside. The four long-running beats also publish
multi-arch container images with SBOM and provenance attestation, signed
keylessly via Sigstore/cosign. Building from source needs Go and
[go-task](https://taskfile.dev): `task build` puts all six binaries in `bin/`.

---

## Standing it up locally

thump runs against four cluster profiles today (`Tiltfile`'s `CLUSTERS` dict):
`ceph-lab` (default), `rook-gke`, `rook-gce-k3s`, and `thump-test`. All four are
rook/Ceph clusters because that's the rig this repo builds and chaos-tests
against, not because thump requires one.

`thump-test` additionally runs the OpenTelemetry Astronomy Shop demo alongside
Ceph — a second, orthogonal domain on one cluster, sharing no signal, failure
class, or catalog action with the first — the general-purpose claim under load
instead of in a test fixture. Bring one up, then:

```sh
tilt up -- --cluster=rook-gce-k3s   # or ceph-lab, rook-gke, thump-test
```

**Dry-run is the default, and you have to opt into anything else.**
`THUMP_EXECUTOR` is `dry` unless you explicitly set it to `live`
(`internal/config/config.go`). In dry mode thump renders every approved decision
in full and touches nothing. Going live additionally requires an armed
`THUMP_KILLSWITCH` file; a disarmed switch reports `ResultBlocked` rather than
silently no-op'ing, so a blocked run is loud. `SLACK_WEBHOOK_URL` is optional —
leave it unset and thump just doesn't page anyone on a hold or a settle.

`calipers incidents` folds the stream into a read-only view of what the engine has
done and what it's holding. `calipers approve <fingerprint>` acks a held action, and
hiss re-issues the approved decision. The operator surface never writes a
decision itself.

There is a second way to release a hold, off by default
(`approvalRequests.enabled`, `deploy/chart/thump/values.yaml`). With it on, hiss
opens an `ApprovalRequest` custom resource per held action and a human releases
it by patching one field:

```
kubectl -n thump get approvalrequests
kubectl -n thump patch approvalrequest ar-8f3c1d2e0a5b7649 \
  --type=merge -p '{"spec":{"decision":"approve"}}'
```

What that buys over `calipers approve` is the approver's identity. `--approver` is a
string an operator types about themselves; under the CR the approver is the
authenticated Kubernetes subject, a `MutatingAdmissionPolicy` stamps it into
`thump.dev/approved-by` regardless of what the patch body said, and the API
server records the request in its own audit log. Every other audit claim this
engine makes is thump attesting about itself. Grant it with
`approvalRequests.approvers`, which is empty by default.

`spec.decision` accepts `approve` and nothing else. Bypassing hiss's risk gate
stays with `calipers force` (D-9): a break-glass verb five characters away from an
ordinary approval, on the same object and under the same RBAC verb, would not be
break-glass. The controller refuses to publish an ack it cannot attribute, so a
cluster that never installed the admission policies fails closed rather than
approving on behalf of nobody. Needs Kubernetes 1.36 —
`MutatingAdmissionPolicy` went stable there.

Check `internal/config/config.go` for the full environment variable list before
arming anything for real.

---

## Invariants (read as law)

Numbered so a review can cite one directly ("this violates I-4"). Full text,
sourcing, and the test that would go red for each:
[`docs/invariants.md`](docs/invariants.md).

1. **Signals describe state, never interpretation.** "p99 412ms vs 38ms
   baseline" is a signal; "system degraded" is a reasoning output. rattle never
   editorializes.
2. **Two confidence numbers, never one field.** Signal-strength confidence — is
   this input trustworthy? — is rattle's. Hypothesis confidence — how sure is
   this diagnosis? — is clank's, computed from the first plus corroboration.
3. **Policy lives only in Governance.** If clank grows an
   `if confidence < 0.8`, policy has become invisible and unauditable. hiss is
   the only policy holder.
4. **The catalog is the autonomy boundary.** Blast radius is bounded by a
   declared action's scope and reversal, never by the reasoner's judgment. A
   candidate outside the catalog is a hard error and never a soft ignore.
5. **Gate ≠ shaper.** The readiness gate is a strict conjunction of minimums —
   `budget ∧ dedup ∧ evidence` — never a weighted sum. A high score on one axis
   cannot buy passage on a failed minimum.
6. **The five belief-formation defenses are not optional.** A ≥2-source
   corroboration floor, freshness-decay on historical alignment, a
   predicted-but-absent signal that decrements rather than staying silent, a
   representable "partially fixed, still diverging" outcome, and a
   forced-live-citation rule on the gate. Together they defend against a cheap
   wrong belief compounding through scoring and memory.
7. **Reasoning selects, Governance permits.** hiss answers exactly one question
   — allowed, right now? — and never re-ranks or substitutes clank's
   recommendation.
8. **Learn is a return edge, not a module.** click is thump's `Outcome` flowing
   back into clank's case base. Wiring, with no boundary-crossing reach of its
   own.
9. **The signal contract owns the `if`.** Freshness bounds, confidence floors,
   exclusion windows all live in rattle's contract, even when the transport is a
   poll ticker. Degraded trust attenuates confidence; it never silently drops
   the signal.
10. **Nothing executes ungoverned.** Every act is gated by hiss *and* the global
    kill switch, defaults to dry, and carries an executed reversal path. Highest
    blast radius gets the most paranoid on-ramp.
11. **The log is the system of record.** Detections and proposals ride the
    stream into an S3-offloaded WAL. etcd holds slow, human-authored config only.
    No CRD-per-noun.
12. **The Trust Ceiling.** No autonomous write authority until real runtime
    Governance, action contracts with automatic reversal, signal contracts with
    declared guarantees, and calibrated confidence are *all four* simultaneously
    operational. Three of four doesn't count.
13. **Every wave stays red→green.** No untested seam crosses into the next beat.
14. **Delivery is at-least-once; identity is the fingerprint.** Every transport
    may redeliver, so every consumer dedupes on the producer-assigned
    fingerprint and never on transport metadata like a filename or sequence
    number.
15. **The operator surface is read-only or evidence-producing.** A human
    interface may read emitted state or emit an ack event, and nothing else. It
    may never write a decision, execute an action, or touch the kill switch. The
    one declared exception is a break-glass `calipers force`: a human, never the
    automated surface, disposing in Governance's place — attributed, audited,
    rendered visibly `forced`, and still kill-switch-gated.

---

## Known-open

- **The `hold` → ack loop is built; the surface around it is thin.** `calipers
  incidents`, `calipers approve`, and the break-glass `calipers force` all work, and
  hiss re-issues a held decision on an ack. What doesn't exist yet is anything
  richer: no drill-down from an incident to the evidence that produced it, no
  live tail, no web view. If you're evaluating the operator experience
  specifically, that's where it actually stands.
- **The model-modulates-the-prior multiplier isn't built.** The authored
  `severityReductionPct` baseline is stamped onto every candidate and measured
  against the observed reduction. The SAO/topology-aware adjustment *on top of*
  it is still just the plan — today the number is copied verbatim from the
  catalog.
- **Risk shaping is two factors, not a scalar.** `RiskBand` is computed from
  reversibility and blast tier alone. Every cell of that 2×3 lattice is pinned
  by a test, and it hasn't yet decided anything wrong, but it is narrower than
  the composite score the design calls for. See
  [`docs/design-decisions.md`](docs/design-decisions.md), D-3.
- **`accelerate-recovery` assumes nothing else is reconciling the operator it
  pauses.** The action scales `rook-ceph-operator` to zero so its Ceph tunables
  survive the recovery window. That's proven live now — knobs read back `16`/`16`
  mid-flight, window settled `success`, reversal clean. It only holds here
  because our ArgoCD was taught to ignore `spec/replicas` on that Deployment;
  the first live attempt had self-heal putting the operator back in about a
  second. Run this under your own reconciler and you get that failure back, and
  there's no field in `catalog.yaml` that would warn you. See
  [`docs/design-decisions.md`](docs/design-decisions.md), D-10.
- **A chaos-mesh v2.8.3 bug blocks one class of live test.** `toda`, the IOChaos
  fault injector, panics on startup on every OSD we've tried it against. Not a
  config mistake on our side — confirmed against upstream. Until that's fixed or
  worked around, one signal class (OSD I/O latency injected at the FUSE layer)
  can't be chaos-tested end to end.
- **thump's own `ServiceMonitor` gaps have bitten us before.** A missing scrape
  target made a fully-working pipeline look broken from the outside more than
  once. If a live run looks dead, check Prometheus targets before assuming the
  engine is.

---

## Building & testing

Build tooling is [go-task](https://taskfile.dev) (`Taskfile.yaml`) — run
`task --list-all` for the full set.

| Command | What it does |
|---|---|
| `task run:clank` / `run:rattle` / `run:hiss` / `run:thump` / `run:calipers` | Run one beat or the operator CLI |
| `task build` | Build all six binaries to `bin/` |
| `task ci` | Full local CI: fmt-check → vet → lint → vulncheck → chart-lint → race → build |
| `task test` / `task race` | Tests, with `-race` |
| `task coverage` | Coverage profile + total |
| `task vulncheck` | govulncheck over deps |
| `task chaos:preflight` | Check preconditions & disable ArgoCD self-healing before live chaos runs |
| `task eval` | The reasoner eval against the production catalog — key-gated, not part of `task ci` |
| `go test ./test/onboarding -v` | The whole engine over a domain authored in config alone — no key, no cluster |
| `go test ./internal/clank -run TestGate -v` | Run a single test |
| `gotestdox ./...` | Read test names back as a spec — **silently prints nothing on Go 1.26**; use `go test -v` |

For the live scenario matrix, fault mechanisms, and preflights across Ceph and otel-demo, see [`chaos/README.md`](chaos/README.md).

`task ci` green is the definition of done. GitHub runs fmt-check, vet, lint,
vulncheck, chart-lint, and test on every push, but not `-race`, since that
doubles build time and CI minutes cost money. **Run `task ci` locally before
opening a PR.** A green `go test` on GitHub is a weaker claim than a green
`task ci`.

---

## Contributing

The "never" column in the beat table is the design. A policy check inside clank,
a raw payload riding in a conversation message, a recomputed fingerprint, a new
noun that isn't in the vocabulary — those are the regressions that matter most,
ahead of any bug in business logic.

`CONTRIBUTING.md` has the full guide, including the review rule that isn't
obvious: a catalog change is an execution-surface change. Go conventions,
comment style, and testing standards live in `AGENTS.md`; read it before
touching any `.go` file. Security reports go through `SECURITY.md` rather than a
PR.

---

## Documentation

| Doc | What's in it |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | The five beats and four planes, the boundary objects, and a mechanism-level walk of one signal end to end |
| [`docs/invariants.md`](docs/invariants.md) | All fifteen invariants with their sourcing, the violation smell for each, the test that would go red, and the four-question smell test |
| [`docs/onboarding.md`](docs/onboarding.md) | Authoring a domain in config: the seven files, the verbs, the environment, and the guards that catch a mistake |
| [`docs/design-decisions.md`](docs/design-decisions.md) | Where this project knowingly departs from Jambor, and why — including what was declined and what's parked |
| [`docs/c4-architecture.md`](docs/c4-architecture.md) | C4 context/container/component diagrams and a golden-path sequence |
| `AGENTS.md` | Go conventions, comment voice, testing standards |
| `CONTRIBUTING.md` | How to work here, and what gets reviewed hardest |

### Full provenance ledger

- **David Jambor, *Agentic Reliability Engineering*** (O'Reilly) — the
  four-plane architecture, the boundary-object discipline, the incident-response
  loop, and the belief-formation defenses. Also the source of the vocabulary:
  `SignalDetection`, `ProposalSet`, `ActionContract`, the Situational Awareness
  Object, the Trust Ceiling. [`docs/invariants.md`](docs/invariants.md) tags
  each invariant that traces to him;
  [`docs/design-decisions.md`](docs/design-decisions.md) is the ledger of
  departures, including the `requires_human_approval` field I replaced with
  `requested_authority_level` because the original leaks the
  Reasoning/Governance seam.
- **John Arundel, *The Power of Go: Tools*** and ***The Power of Go: Tests***
  (Bitfield) — build method, the functional-core/imperative-shell split in the
  actuator, and the test-naming discipline `gotestdox` reads back.
- **Joel Holmes, *Shipping Go*** (Manning) — release pipeline, delivery, and
  repo layout conventions.

The dated design journal — day-by-day investigation notes, live-run
post-mortems, per-phase build plans — stays private rather than mirrored here.
Everything load-bearing from it is in `docs/`.
