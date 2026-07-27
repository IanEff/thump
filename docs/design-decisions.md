# thump — design decisions

This engine is built from a book — *Agentic Reliability Engineering* — and it
does not follow it everywhere. This file is the record of where it doesn't, and
why.

The rule the project holds itself to:

> **A divergence is fine when it's written down. Drift is a divergence that
> isn't.** Write the entry *before* you diverge, not after someone notices.

Two things this file is not. It isn't a changelog (that's `CHANGELOG.md`), and it
isn't the dated working journal — the day-by-day trail of what was tried, what
broke on a live cluster, and what got decided at 11pm lives in the author's
private notes. Pointing you at a file you can't open would be worse than saying
that plainly. **What's load-bearing has been brought here in full**, so nothing
below requires the journal to make sense.

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

**We do:** `trim force <fingerprint>` lets a *human* emit an approved decision
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
