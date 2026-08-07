# thump — design decisions

This engine is built from a book — *Agentic Reliability Engineering* — and it
does not follow it everywhere. This file is the record of where it doesn't, and
why.

The rule the project holds itself to:

> **A divergence is fine when it's written down. Drift is a divergence that
> isn't.** Write the entry *before* you diverge, not after someone notices.

Two things this file is not. It isn't a changelog (that's `CHANGELOG.md`), and it
isn't the dated working journal — the day-by-day trail of what was tried and what
broke on a live cluster stays in private notes. Everything load-bearing from it
is here in full, so nothing below needs the journal to make sense.

Numbering is stable and gappy on purpose — entries keep their original IDs so a
review comment citing "D-10" stays valid.

**Status vocabulary:** **Ratified** — decided, and the code does it.
**Parked** — the book says something we haven't built; named so it isn't
mistaken for an oversight. **Declined** — considered and rejected, with the
reasoning kept so it doesn't get re-derived.

---

## D-1 · The log is the system of record, not etcd — **Ratified** (2026-07-01)

**The book says:** detections and proposals are Kubernetes custom resources,
written to etcd.

**We do:** the beats deploy as operators, but their outputs ride a NATS
JetStream stream and land in an S3-offloaded write-ahead log. etcd holds slow,
human-authored config only. There is no custom resource per noun.

**Why:** a CRD per boundary object turns etcd into a hot data path it isn't built
for, and turns every schema change into an API-machinery exercise. More
importantly it makes the audit trail a *derived* thing — you'd be reconstructing
"what did the system believe at 14:32" from a sequence of resource versions.
Making the log primary means the trail is the artifact, and every reporting view
is a read-model you can throw away and rebuild.

*See [I-11](invariants.md#i-11--etcd-never-sees-the-data-path).*

## D-2 · Identity-only envelope — **Ratified** (2026-07-05)

**The book says:** the measurement `Envelope` carries `Metric()`, `Target()`,
`Window()`, and `Baseline()` registry references.

**We do:** an identity-only shape — affected object, declared tier, contract,
kind — with the registry references left out.

**Why:** the fuller envelope leans on a metric registry the book itself only
sketches, and committing to those accessors early would have pulled a
half-designed relational model into the wire format. The narrower shape has now
survived two beats and a live run without ever wanting the registry refs, which
is the evidence we were waiting for. It stays narrow.

## D-3 · Risk shaping is reversibility and blast tier only — **Partially built**

**The book says:** a composite risk score derived on the situational-awareness
object, with four normalizers and a band map.

**We do:** `RiskBand` is computed from exactly two authored facts —
does the action have a reversal path, and what blast tier did a human author it
at. The **third verdict** the fuller design called for (`hold`: approved in
principle, waiting on a human ack) **is built**, along with the operator path
that satisfies it. The richer scalar underneath it is not.

**Why partial:** the third verdict was the part with a real gap behind it — before
it existed, an action that was eligible but too wide to fire unattended had
nowhere to go. The composite scalar is a tuning improvement on a decision the
two-factor lattice already makes correctly, and every cell of that 2×3 lattice is
pinned by a test. It gets built when a real case shows the lattice deciding
something wrong, not before.

*See [I-5](invariants.md#i-5--gate--shaper).*

## D-4 · Blast-radius acceleration — **Parked**

**The book says:** blast-radius math — `d(affected_pct)/dt` — folds into the
Detect plane.

**We do:** rattle's detectors deal in burn rates. `BlastRadius.Velocity` is a
coarse label ("accelerating", "fast"), not a computed derivative.

**Why:** the coarse label is what the ranker actually reads, and it's been
sufficient. A real derivative needs a sampling story rattle doesn't have yet.

## D-5 · `ValidationState` on a ticker — **Parked**

**The book says:** the delivery pipeline emits a periodic `ValidationState` for a
Test Agent to consume.

**We do:** the loop logs detections. There is no test agent.

**Why:** that's agentic-delivery-pipeline territory (the book's chapter 8), and
this engine is an incident-response loop. It's a coherent thing to build; it
isn't this thing.

## D-6 · Flat baselines — **Parked**

**The book says:** dynamic baselines are the Signal plane's job.

**We do:** `EnvelopeDetector` uses a flat trailing mean ± Kσ window.

**Why:** the book is thin here, and so is the wider ecosystem — mature SLO
tooling largely doesn't do dynamic baselining either. rattle owns this when
there's a real reason to, and the window is the honest version until then.

## D-7 · Outcomes return directly to Reasoning — **Ratified** (2026-07-02)

**The book says:** outcomes re-enter the loop through Signal-plane revalidation.

**We do:** thump's `Outcome` goes straight back into clank's case base via
click's return edge. The revalidation hop is parked along with the convergence
watcher that would feed it.

**Why:** the revalidation hop's value is that a claimed outcome gets independently
re-observed before it's believed. thump's convergence watcher already does that
— it re-reads telemetry after the success window before settling the outcome — so
routing through Signal would add a hop without adding the property it exists for.

*See [I-8](invariants.md#i-8--learn-is-the-return-edge-not-a-module).*

## D-8 · thump executes — **Ratified** (2026-07-15), proven live (2026-07-16)

**The prior rule was:** thump v1 is dry-run by construction. It renders the
concrete action and stops.

**We do:** a `Live` executor, behind hiss's verdict **and** a global kill switch,
with automatic reversal actually executed rather than merely stamped on the
record.

**Why:** "nothing executes" is a stronger claim than "nothing executes
ungoverned," but it's also a claim you can hold forever without ever finding out
whether the governance works. The trust ceiling
([I-12](invariants.md#i-12--the-trust-ceiling)) names the four mechanisms that
have to be simultaneously operational before write authority is granted; the
fourth one — reversal that has actually fired — could not be demonstrated without
crossing this line. It was crossed once all four were real, and the first live
proof was an unmet success window firing a real undo on a real cluster.

**A detail worth recording:** the actuator reaches the cluster through
**client-go** (`remotecommand` exec, dynamic-client merge-patch), not by shelling
out to `kubectl` as the original plan had it. A distroless container can't shell
out. So client-go compiles into the binary, and the containment property is
preserved a different way: infrastructure-reaching code lives *only* in
`internal/actuate`, never in package `thump`, and a structural test
(`TestThumpCannotReachInfrastructure`) enforces it. The tripwire's spirit intact,
its letter changed.

## D-9 · A break-glass force path — **Ratified** (2026-07-18)

**Our own rule said:** the operator surface never writes decisions
([I-15](invariants.md#i-15--the-operator-surface-never-disposes)).

**We do:** `calipers force <fingerprint>` lets a *human* emit an approved decision
that bypasses hiss's risk gate — but **not** the kill switch (a disarmed switch
still blocks the forced order) and **not** the audit trail (every forced decision
is auditable, operator-attributed, and rendered visibly `forced` in every view).

**Why:** the alternative to a designed break-glass path isn't no break-glass path
— it's someone editing YAML on a cluster at 3am with no attribution. Making the
override a first-class, audited operation is strictly safer than pretending it
won't happen. The boundary that matters is preserved exactly: **force overrides
governance, never the physical stop.**

## D-10 · Pausing the reconciler that owns the value — **Fixed and proven live** (2026-07-26)

**The problem:** a governed, executed action is supposed to durably change
cluster state for the length of its success window. One shipped action didn't.
`accelerate-recovery` set two Ceph tunables via the toolbox, and Rook's own
operator reconciled them back to its CR-declared values within roughly 29
milliseconds — faster than any success window could observe. The action was a
no-op that reported success.

**What we do now:** the forward is a four-step sequence
(`config/actions/catalog.yaml`). Scale the `rook-ceph-operator` deployment to
zero, set `osd_mclock_override_recovery_settings true`, then set the two
tunables. The reverse restores the tunables first, clears the override, and
unpauses the operator last, so Rook comes back to find its declared values
already in place.

The middle step is the second bug, found after the first fix shipped. Rook has
defaulted to `osd_op_queue=mclock_scheduler` since Quincy, and mclock discards
`osd_max_backfills` and `osd_recovery_max_active` unless that override is set
first. Both `ceph config set` calls returned exit 0 and changed nothing. The
action was a no-op for a second, unrelated reason through one whole live session
before anyone read the values back by hand.

**Proven live 2026-07-26** on `slo_burn:ceph-cluster/1785101992930477260`: both
knobs read back `16`/`16` mid-flight, the window settled `success` with
`observedSeverity` 0, the reversal restored the pre-test defaults, and the
operator came back `1/1` with `HEALTH_OK`.

**One failure in getting there is not ours to fix, and the contract cannot warn
you about it.** On the first live attempt the pause did not hold: ArgoCD's own
self-heal reverted `replicas: 0` in about a second. It holds now only because
the rig was changed to stop fighting it — `ignoreDifferences` on `spec/replicas`,
plus removing a competing ApplicationSet-generated Application. That is a
property of one deployment. If you run this action under your own reconciler,
you get the bug back, and nothing in `catalog.yaml` can tell you so.

**Why this shape and not a new verb:** `scale` is an already-compiled mechanism,
and the actuator already collapses a multi-step list into a sequence. Config
picks mechanisms it already has, so
[I-4](invariants.md#i-4--the-catalog-is-the-autonomy-boundary) is untouched and
the autonomy boundary does not widen by one inch.

**The tradeoff, said out loud:** this action now pauses the cluster's storage
operator for the duration of a recovery window. That is why the contract is
authored `blastTier: high`, and it is why D-11's restore path is a correctness
property rather than hygiene.

**The route not taken.** The architecturally cleaner fix is to author the config
where Rook *honors* it — `CephCluster.spec.cephConfig` — which needs a generic
`patch` verb: an authored group/version/resource plus a merge-patch body.
**Declined for now, deliberately.** D-12a's argument for letting `exec` ride
config is that thump's ServiceAccount RBAC is the real bound. That argument gets
materially thinner when config can name *any resource in any API group* — `exec`
is bounded by which pods the ServiceAccount can reach, but a generic patch verb
is bounded only by the full breadth of its write permissions, and reviewing a
catalog PR stops being "does this argv make sense" and becomes "does this GVR
exist and what does patching it do." That's a real widening, and it deserves its
own decision rather than riding in as the incidental cost of fixing a storage
knob.

The declination originally carried a reopen clause: if the pause turned out not
to hold, `patch` came back. It held, through a real recovery window, with the
knobs read back by hand. The route stays declined on that evidence rather than
on the clause.

## D-11 · Reversal after success — **Fixed** (2026-07-26)

**The problem:** reversal is the safety net for a temporary action once its
window elapses — but the watcher only fired it on **failure**. Success and
reversal were mutually exclusive. A temporary tuning change that *worked* stayed
applied forever, with nothing watching it.

Combined with D-10, that was worse than the bug it replaced: a successful
acceleration would have left the cluster's storage operator scaled to zero,
indefinitely, by an action that reported success.

**What we do now:** `contract.Reversal` carries an authored
`restoreOnSuccess: bool`. `ReversalWatcher.Watch` returns a `Settlement` with
`Converged` and `Fire` as **separate** fields, so a met window with
`restoreOnSuccess: true` fires the undo while still settling the outcome as
`success` — not `partial_non_converging`.

**Why it's an authored fact and not an inference:** the watcher cannot know
intent, and shouldn't guess. `disable-cart-failure` succeeding means *stay
disabled* — the flag flip **is** the remediation. `accelerate-recovery`
succeeding means *put the knobs back* — the tuning was temporary by nature. Same
convergence verdict, opposite correct behavior. That distinction belongs to the
action's author, which puts it on the action contract where every other authored
fact lives.

Two of the six shipped actions set it. The judgment is genuinely per-action —
`throttle-non-critical-paths` is temporary too, but restoring baseline replicas
the moment the window closes would re-create the fault it just cleared.

**A related fix:** `Actuator.Render` now carries catalog-sourced reversal fields
onto the order **unconditionally**, rather than inside a guard on a field clank
proposes. A fact authored in the catalog must not be droppable by a producer that
doesn't own it.

## D-12 · The action→mutation binding is config — **Ratified** (2026-07-24)

**Our own prior position (2026-07-17) was:** the binding from a catalogued action
to a concrete cluster mutation stays in Go. Data-driving it makes the catalog an
RCE-by-config surface and widens the autonomy boundary.

**We do:** the binding **table** is a per-action `execution` block in
`config/actions/catalog.yaml`. The mutation **mechanisms** — `scale`, `restart`,
`flagVariant`, `exec` — stay bounded, tested Go in `internal/actuate`.

**Why the reversal:** config is *already* the authored autonomy boundary. Whoever
authors the catalog already declares what may be proposed and executed at all.
Letting them also declare *which compiled mechanism* an action uses is the same
trust domain, not a new one — and it's what makes "onboarding takes no Go" true
rather than nearly-true. Consistent with D-8: infrastructure-reaching code still
lives only in `internal/actuate`. Only the table is data; never the mechanism.

## D-12a · `exec` rides config too — **Ratified** (2026-07-25)

**D-12 as first written said:** raw `exec` — arbitrary argv — is excluded from
the config surface. The toolbox escape hatch stays in Go.

**We do:** the `exec` verb is authored in config, argv and all.

**Why the exclusion didn't survive:** two of the six shipped actions *are*
toolbox exec. "The YAML is the single source of truth" was unreachable without
it, and the alternative was a Go escape hatch keyed by action name — which
defeats the entire point.

**What actually bounds an authored `exec` step is not the verb list.** It is
thump's ServiceAccount RBAC (`pods/exec`, scoped per namespace), the global kill
switch, and hiss's policy. Whoever can merge `catalog.yaml` can run argv in any
pod that ServiceAccount can reach.

**So: a catalog PR is an execution-surface PR, and it is reviewed as one.** See
`CONTRIBUTING.md`.

A compiled (namespace, selector) allowlist for the exec verb was considered and
declined — it buys little while RBAC is already the real bound, and it can be
added the day untrusted catalog PRs start arriving.

*This entry closed a **drift**, not a fresh divergence: the code shipped this
behavior while the record still said the opposite. Which is exactly the failure
mode this file exists to catch.*

## D-13 · Every outbound client gets an audited retry policy, timeout, and a failure that surfaces — **Ratified** (2026-07-28)

**The finding this generalizes:** `internal/broker/connect.go`'s NATS client
shipped with none of the three — an unbounded finite reconnect budget, no
timeout on the notion of "still connected," and a `Closed` state nothing
signaled (fixed separately; see `beat.BrokerHooks`). That was one instance of
a class: every third-party client this engine dials was trusted on its
defaults, not audited against them. This entry is the sweep, one row per
client, verified against each library's own source rather than assumed.

| Client | Seam | Retry policy | Timeout | Failure surfaces |
|---|---|---|---|---|
| `nats.go` | `broker/connect.go` | `MaxReconnects(-1)` — effectively-infinite, authored | dial timeout + `AckWait` (broker.go) | `OnDisconnect`/`OnReconnect`/`OnClosed` hooks; readiness follows the connection |
| `client-go` | `actuate/kube.go`, `actuate/runner.go` | client-go's own default (`rest.Request`, `maxRetries: 10`, retries 429/5xx honoring `Retry-After`) — already authored by the library, unaudited before this entry | **was unbounded** — `rest.InClusterConfig` sets no `Config.Timeout`, and thump's `Transport.handle` never calls the redelivery-preventing `heartbeat` on the live-execute path. **Fixed**: `actuate.actuateTimeout` (20s) now bounds every `Runner.Run` call, comfortably inside the 30s `AckWait` a hung call would otherwise blow through | error return, wrapped and recorded as a `Result: failure` `Outcome` with text — was already true for a returned error, now also true for a timeout instead of a hang |
| Anthropic SDK | `clank/model_anthropic.go` | SDK default, `MaxRetries: 2`, retries 429/5xx — already authored by the library | `option.WithRequestTimeout(120s)`, already authored | `Engine.Propose` wraps and returns the error; the reason loop's own heartbeat (unlike thump's) keeps a slow-but-alive call from tripping `AckWait` |
| AWS SDK v2 (S3) | `beat/objectstore.go` | SDK default standard retryer, `MaxAttempts: 3`, retries 429/5xx/throttling — already authored by the library | `beat.RunShipper`'s `PollLoop` wraps every ship tick in `WithTimeout(4×ShipInterval)` (120s) — already authored, at the right altitude for "however many segments are sealed this tick," not per-call | logged via `slog.Error("tick failed", ...)`; a segment that fails to ship is left on disk for the next tick, not silently dropped |
| OTLP (`otlptracegrpc`) | `internal/otelx/trace.go` | **known-bad, not fixed here.** A wrong/stale CA surfaces to gRPC as `codes.Unavailable`, and `otlptracegrpc`'s vendored default retry policy treats `Unavailable` as retryable — it backs off and keeps retrying up to the export's own 10s timeout ceiling before giving up on that batch, silently, per batch, forever | `WithTimeout` default (10s) per export, already authored | **does not surface** — async `WithBatcher`, no log line at the point of failure; spans just stop arriving. Flagged, not actioned — needs a `/readyz`-adjacent liveness check or an explicit ops-runbook entry, a bigger change than this sweep's scope |
| `httpx` | `internal/httpx` | none authored — every current caller (Prometheus, Loki) is a single request, not a multi-step operation a partial retry could corrupt | `DefaultBackendTimeout` (10s), authored and centralized | caller's own error path; every backend call already goes through this one seam, which is the precedent the other rows are held to |

**What actually shipped, this entry:** the client-go fix
(`internal/actuate/runner.go`'s `actuateTimeout`, `internal/actuate/runner_test.go`'s
`TestRunner_TimesOutAHungMutationRatherThanBlockingForever`) — the only row
that was silently wrong, and the only client on this list that mutates a
cluster. Every other row was already correctly postured; this entry is their
first audit against source, not a behavior change.

*Now ratified as [I-17](invariants.md#i-17--every-client-this-engine-dials-has-an-authored-retry-policy-timeout-and-a-failure-that-surfaces).*
The OTLP row above is still an open counterexample — and it is the *trace* leg
specifically; `internal/otelx/otlpbreaker.go` closed the equivalent hole on the
metrics leg, where a permanently-unimplemented `MetricsService` used to be
retried and logged every collection interval forever. A diagnostic path now
exists for the trace leg:
`docs/runbooks/otlp-silent-failure.md` walks the symptom — spans stop arriving,
no log line, beat stays `1/1 Running` — to the check that confirms it.

## D-14 · The `ApprovalRequest` CRD earns its way past D-1 — **Ratified** (2026-07-30)

**Our own rule said:** D-1 keeps detections, proposals, and decisions off
etcd. The one door left open was a graduation clause — a CRD is earned by a
demonstrated reconciliation or status need, never granted by default.

**We do:** `ApprovalRequest` (`internal/hiss/approvalrequest.go`) is the one
CR that walks through that door, and it took two tries to name why. The first
justification offered was durability: `PendingHolds` is an in-memory map, so
a hiss restart drops every pending hold and a late human approval lands on
nothing. That's a real bug, but it doesn't need Kubernetes — it's fixed by
rebuilding the holds from the stream on startup (`internal/hiss/rebuild.go`),
which is a read-model, same as everything else in the audit trail. What
survived the second look is a property no amount of stream replay can buy:
today, `Approval.Approver` is whatever string a human typed at `calipers approve
--approver` — self-asserted, the same posture as a break-glass decision under
D-9. Under the CR, the approver is the authenticated Kubernetes subject the
API server stamped onto the patch, and the authority to approve is RBAC on
the resource, not a flag a caller chose to pass.

**Why the CRD stays this narrow:** `ApprovalRequestSpec.Decision` accepts
exactly `""` or `"approve"` (`internal/hiss/approvalrequest.go`); anything
else is rejected. A `force` value was shipped once, publishing straight to
`thump.decisions`, and it was reverted — a `kubectl patch` five characters
from `approve`, on the same object, under the same RBAC verb, is not
break-glass, it's the normal path with a typo standing between it and the
gate. Overriding the risk gate stays `calipers force`'s job (D-9): its own
binary, its own subcommand, a required `--approver`. `ApprovalRequest` only
ever emits an ack; hiss still governs exactly once, and the WAL stays the
audit trail, not this CR.

## D-15 · Cross-source correlation needs its own vocabulary test — **Fixed and proven live** (2026-07-30)

**The problem:** `ChangeEvent.Target` and `NodeState.Name`
(`api/v1/proposal/proposal.go`, `sao.go`) are both plain `string`, and
`findNode` (`internal/clank/causal.go`) joins them by equality. A change
source that emits names from a vocabulary the topology graph never uses
still compiles, still runs, still scores every event zero — and a
composition-root guard that only asks "is this collaborator wired in"
answers yes, because it is. On `thump-test`, ArgoCD `Application` names and
whir's topology node names (`config/thump-test/whir/catalog-info.yaml`)
intersected on 2 of 24 nodes.

**What we do now:** every cross-source correlation gets a test asserting a
real match against the shipped topology catalog
(`internal/clank/clank_test.go`'s
`TestArgoChangeSource_TargetsShareTheTopologyCatalogsVocabulary`), not a
hand-written fake standing in for it. `CausalScore.InTopology` carries the
join's outcome onto the emitted set, so a failed join shows up on the audit
trail as a fact rather than as a confidence number nobody can explain.

**Why this needed its own test class:** a wiring guard proves a collaborator
isn't a no-op. It can't prove the collaborator resolves against the right
vocabulary — a fully-wired change source that correlates with nothing passes
it clean.

## D-16 · The causal term became an additive bonus — **Ratified** (2026-07-31)

**The problem:** `scoreConfidence`'s causal term multiplied computed
confidence by `Likelihood`, and `LikelihoodOK` is false whenever no change
event resolves into the signal's own topology. A run holding no change data
at all kept its full confidence; a run holding a change event that genuinely
corroborated the signal got multiplied down. The engine was least confident
exactly when it had the most to go on, which makes wiring any new change
source a regression by construction.

**We do:** `groundedConfidence` (`internal/clank/confidence.go`) adds the
causal term instead of multiplying by it — `computed =
min(1, computed*(1+w.Causal*in.Likelihood))` when `LikelihoodOK`, unchanged
otherwise. `ScoringWeights.Causal` is authored at 0.5, so a maximally
implicated in-topology change can raise confidence by up to 50%; an absent
one costs nothing.

**Why additive and not just a smaller multiplier:** any factor bounded above
by 1 still pulls confidence down whenever the term is present but weak — the
same trap, just less severe. Only an additive term with an `OK` gate that
drops it out entirely, rather than letting it read as zero, keeps an absent
correlation neutral. The model's self-report still applies afterward as a
ceiling (`scoreConfidence`), so the causal bonus can raise a candidate toward
what this run grounded, never past what the model was willing to claim.

## D-17 · The deployment manifest is a second composition root — **Ratified** (2026-08-01)

**The problem:** a Go-level wiring guard proves the code publishes to a
subject and a collaborator consumes it. It has no view of whether the Helm
chart actually grants that subject, or whether a NetworkPolicy lets the
dial reach its target at all — a manifest gap fails outside every test `task
ci` runs. This class of bug found seven real instances on one rig: a missing
CiliumNetworkPolicy, missing NATS grants on `thump.approvals` and
`thump.held`, and `thump.declines` granted to the wrong user entirely. Some
were silently masked rather than loud — a local WAL write succeeds while the
message never reaches the stream, and a NetworkPolicy that blackholes a dial
hangs instead of erroring.

**We do:** guards over the deployment plane are derived from the code,
never hand-kept mirrors of it. `internal/broker/grants_test.go` walks each
beat's `go/ast` for subject literals passed to `Publish` and checks both
directions against the rendered `nats.conf` — a beat publishing to a subject
nobody granted it, or granted a subject it never publishes to, fails either
way. `internal/beat/chart_test.go` derives which beats link client-go
(`go list -deps ./cmd/<beat>`) and checks each one has the Cilium egress
policy that client-go's dial needs.

**Why this is its own entry and not folded into the wiring-guard class
already in place:** a Go composition-root guard and a manifest are two
different sources of truth for the same fact, and nothing forced them to
agree. The seventh grant gap — `thump.declines` on the wrong user — was
caught by the guard in the time it took to write it and run it once, before
any deliberate regression was introduced. A hand-maintained list is the
thing that fails; a derived one can't drift from what it derives from.

## D-18 · What a scoring constant encodes decides whether it's Go or config — **Ratified** (2026-08-01)

**Our own prior position was:** clank's scoring constants
(`internal/clank/weights.go`) stay in Go, because they're clank's numbers to
own.

**We do:** ownership was the wrong axis — hiss's confidence floors are
authored data too (`config/hiss/policy.yaml`, loaded by `hiss.LoadPolicy`,
required to start) and still hiss's. The axis that matters is what the
number encodes. A constant that encodes a charter invariant stays a Go
constant with a doc comment saying so —
`minCorroboration = 2` (`internal/clank/casebase.go`) *is* the ≥2-source
floor, and a configurable belief floor is a configurable invariant. A
constant shared with the deployment chart stays Go too, because splitting
one value across a config file and a chart template makes two sources of
truth for one name — `maxDeliver` (`internal/broker/broker.go`) is the
worked edge case: it looked like a config candidate until it turned out that
`EnsureTopology` and each beat's `Run` compare against it from separate
processes, so a config file would have had to reach both. Everything
else — scoring weights, floors, half-lives — is cluster-shaped: how fast a
deploy stops being a suspect depends on deploy cadence, how many cases the
case base holds depends on scale. That moved to
`config/clank/weights.yaml` and `config/hiss/policy.yaml`.

**Why the extraction changed nothing on its own:** `ScoringWeights` and
`hiss.Policy`'s values shipped unchanged by the move, pinned by a
hand-transcribed `...BeforeExtraction()` test literal rather than a call
through the loader under test. Moving a number to config and changing what
it's set to are two different acts, and this entry is only the first one.

## D-19 · A tuned number carries its basis, or it's drift — **Ratified** (2026-08-03)

**Our own prior position was:** D-18's extraction ships the existing value
unchanged, so a number in `config/clank/weights.yaml` or
`config/hiss/policy.yaml` is self-evidently the constant it replaced.

**We do:** a value in either file is either D-18's unchanged extraction,
pinned by its `...BeforeExtraction()` literal, or it carries a written basis
in the file's own comments — the corpus row or measurement that moved it,
where the next reader hits it. A value with neither is a seed, and a seed
that stops saying so is drift. The seeded floors in
`config/hiss/policy.yaml` were the case that found this: they were honest
when written, naming a blocker for why they hadn't been calibrated, and
became misleading only because the blocker (no live executor to generate
calibration data) expired weeks before the comment did.

**Why this rule cuts both ways, worked example:** the first thing it
authorized was a decision *not* to move a number. `redundancy_degraded`
stayed at 0.3 because the mined corpus held exactly two settled incidents on
that class — 0.8500 and 0.8660, both above every candidate floor between 0
and 0.85. Two points that close together can't distinguish a floor of 0.3
from 0.75; moving it on that evidence would be a curve fit wearing a
calibration's clothes. That reasoning is written into
`config/hiss/policy.yaml` itself, next to the value, not filed separately.
A track that measures and declines to turn the knob counts as closed.

## D-20 · Weights are authored, not learned, until the corpus holds N real settled cases — **Ratified** (2026-08-07)

Today, N = 1.

For a reasonable trust curve, that number needs to be something closer to
20–30. For now, authoring keeps expectations sane. `rca`/`replay`/`tune`
don't stop existing — they stop being tuners and become the thing that
measures N and guards against regression. N = 20–30 is the re-entry
criterion: the number that has to be true before calibration reopens on
evidence instead of on vibes.

## D-21 · A cancelled context is not a context — **Ratified** (2026-08-07)

A composition-root guard asks *is it wired?*; D-15 asks *is it wired to the right
thing?*; D-17 asks *can the deployment reach it?* None of the three can ask
whether the context handed to a call is still alive when the call runs —
and a shutdown path is where that question always matters, because the
caller's context is by construction the thing that just died.

**We do:** every shutdown-path I/O in this engine builds its own context —
`context.WithoutCancel` plus an authored timeout — rather than inheriting
one, and never discards its error. `WAL.Drain` is the worked example:
correctly wired on all four beats, deferred, real, and shipping nothing
since the day it landed until this rule was applied to it.

## D-22 · An artifact is a contract with your future self — **Ratified** (2026-08-07)

**We do:** `clank.Case` and `clank.Corpus` carry a `Version` field
(`CorpusVersion = 2`) and JSON tags, `internal/corpus/mine.go` gained
`migrateLegacy` and `ErrUnknownCorpusVersion`, and `CollapseCases` replaced
the per-record rows so one incident is one case. The committed artifact went
from six unreadable cases to two real ones. A file this engine writes and
later reads back — a corpus, a transcript, a fixture — declares its own
version and refuses to silently misread an older shape it no longer means.

## D-23 · A replay that improvises is worse than no replay — **Ratified** (2026-08-07)

A recorded run re-executed for tuning is only evidence if the recording is
the whole input: the moment a replayed loop answers a turn the transcript
doesn't hold, or reconstructs an `EvidenceRef` field the record lost, every
number downstream is attributed to evidence that never existed — and it is
attributed *confidently*.

**We do:** a replay refuses on exhaustion rather than continuing, and
reconstructs no field it cannot read. A gap in the record is fixed at the
recording end, never guessed at the reading end.

---

## Departures from other source material

The D-ledger above is indexed against one book, *Agentic Reliability
Engineering* — that's what this file exists to track. But some packages in
this repo are built directly off a different book's own chapter, and depart
from *that* source too. Recorded here rather than in the numbered ledger,
because folding a Jambor departure and a departure from an unrelated book into
the same numbering would make "which book?" a question every citation had to
answer.

### `tlsx` · Two named constructors, not one overloaded function

**The source says:** Travis Jeffery's *Distributed Services with Go*, ch. 5
("Secure Your Services"), builds a single `SetupTLSConfig(TLSConfig{Server:
bool})` that returns whichever `*tls.Config` the caller needs — client or
server — depending on the bool. The chapter's own margin note already flags
the shape as a wart: "one overloaded function instead of three
constructors."

**We do:** `internal/tlsx` exports two named constructors, `Client` and
`Server`, each returning exactly the `*tls.Config` its name promises. Neither
takes a parameter that reshapes what comes back.

**Why:** a bool that changes what a function returns is two functions wearing
one name — the caller has to read the argument to know what they're getting,
and a wrong bool at the call site still type-checks; it just builds the wrong
config. Two named constructors make that mistake unwritable: a caller that
wants a server config calls `Server`, full stop. This isn't a departure from
a well-considered choice so much as taking the side of the book's own
footnote — the chapter already named the fix, `tlsx` just took it.

---

## Declined, with reasons

Kept so they don't get re-derived from scratch.

- **A generic `patch` verb.** See D-10. Reopened by evidence if the
  operator-pause route fails on a live cluster, not by preference.
- **The race detector in GitHub CI.** `task ci` runs `-race` locally and that's
  the gate. Doubling every push's build time to catch races the local gate
  already catches is money for nothing. `CONTRIBUTING.md` is the decision of
  record.
- **A hard coverage floor.** CI prints a coverage total; it doesn't gate on one.
  A percentage floor on a repo whose wiring code is legitimately untestable
  mostly teaches coverage-farming. Watch it; gate it if it drifts.
- **An httptest SPDY server to cover `Exec`'s stream half.** It would test
  client-go's `remotecommand`, not thump, and buy a green number for no claim.
  The exclusion is documented in the code that stops short of it, rather than
  left to inference.
