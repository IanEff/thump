# Onboarding a domain to thump

Making thump reason about, govern, and remediate a system it has never heard of
takes **no Go**. Seven authored YAML files are the whole surface for that.
Detection is one further requirement, live-only and outside those seven files —
see the note at the end of §3.1.

`test/onboarding/onboard_test.go`'s
`TestOperator_OnboardsANewDomainInConfigAlone` onboards a synthetic service
called `acme` from `test/onboarding/testdata/acme/` and drives it through every
beat — signal, reasoning, governance, execution — as part of `task ci`, with no
API key and no cluster.

The fixture is deliberately named `acme`, and it is deliberately not Ceph,
OpenTelemetry, or anything else the engine ships config for. It's a tripwire: the
day onboarding needs a domain-specific branch in Go, that test is where it
surfaces.

If a step here doesn't work, that's a bug worth reporting rather than something
you're doing wrong.

**The map:** [get the binaries](#1--get-the-binaries) ·
[what you're authoring](#2--what-youre-actually-authoring) ·
[the seven files](#3--the-seven-files) ·
[the execution verbs](#4--the-execution-verbs) ·
[wiring it up](#5--wiring-it-up) ·
[checking your work](#6--checking-your-work) ·
[going live](#7--dry-run-then-live)

---

## 1 · Get the binaries

Tagged releases ship prebuilt archives for linux and darwin, amd64 and arm64,
bundling seven binaries — `rattle`, `clank`, `hiss`, `thump`, `trim`, `bootstrap`,
and `unseal`:

```sh
gh release download v0.1.0 --repo IanEff/thump --pattern '*_linux_x86_64.tar.gz'
tar xzf thump_0.1.0_linux_x86_64.tar.gz
```

Checksums are published alongside them. If you'd rather build from source, you
need Go and [go-task](https://taskfile.dev):

```sh
git clone https://github.com/IanEff/thump && cd thump
task build          # all seven binaries into bin/
```

The four long-running beats also publish multi-arch container images with SBOM
and provenance attestation, signed keylessly via Sigstore/cosign. `trim` doesn't
— it's an operator-run CLI, not a cluster service.

Nothing in the rest of this document needs a cluster until §7.

---

## 2 · What you're actually authoring

Before the file list, the shape of the job. You are answering seven questions,
and every one of them is a question about **your** system that the engine cannot
answer for you:

| You author | The question it answers |
|---|---|
| the watch list | what should I be watching, and when is a divergence real? |
| the dependency graph | what does this thing depend on? |
| state queries | is each dependency healthy *right now*? |
| evidence queries | what may the reasoner look at, and what is each result *about*? |
| failure classes | what does each kind of failure *mean* here? |
| the action catalog | what may be done about it, and how is each thing done? |
| the policy | how sure must we be, and how much may fire unattended? |

Notice what isn't in that list. You never author *how to reason*, *what to
conclude*, or *when to approve*. Those are the engine's, and keeping them out of
config is what makes the safety properties portable across domains.

**Where files live.** Per-site config goes under `config/<site>/`. The action
catalog, failure classes, and governance policy are global:
`config/actions/` and `config/hiss/`. Copy `test/onboarding/testdata/acme/` as
your starting skeleton and read it alongside this document:

```sh
go test ./test/onboarding -v
```

---

## 3 · The seven files

### 3.1 · `rattle/watch.yaml` — the steady-state contract

The SLOs to poll, and the dependencies whose health gates *trusting* a divergence
on this object.

```yaml
version: v1
slos:
  - id: acme-api-availability
    object: acme-api
    tier: tier-1
    objective: 0.99
    contractRef: acme-api-availability:v1
    dependencies:
      - {name: acme-db, role: blocking}
      - {name: acme-cache, role: optional}
```

- `tier` is what governance indexes its confidence floors and band ceilings by.
  A tier with no policy entry is a hole — see §3.7.
- `contractRef` names the signal contract this SLO's divergences declare.
- Dependency names must match a `metadata.name` in your topology file **exactly**
  (the `resource:`/`component:` prefix is stripped before comparison).
- `role: blocking` means a degraded dependency **attenuates** confidence in this
  signal. It lowers it; it never silently drops the signal. A dropped signal and
  a healthy system look identical from downstream, and that's the failure mode
  the whole engine is organized against.

> **An 8th requirement, outside these seven files: a live burn-rate source.**
> `rattle`'s `PromSource.BurnSamples` (`internal/rattle/source.go`) queries
> Prometheus for `slo:current_burn_rate:ratio{sloth_id=%q}`, keyed by **this
> file's `id` field** — not `contractRef`, which is a pass-through string with
> no code linkage to Prometheus; it only resolves the action-catalog entry.
> Nothing in the seven authored files produces that series. You need a Sloth
> `PrometheusServiceLevel` CR rendered with `sloth generate` (service + name
> concatenated as `{service}-{name}` must equal this `id` exactly), or an
> equivalent hand-written recording rule, applied to the cluster. Skip this
> and every other file can be authored correctly and rattle will still never
> detect anything — dry run and `task ci` won't catch the gap either, since
> the onboarding proof test fabricates a `signal.Detection` directly rather
> than exercising `rattle.Reconcile`.

### 3.2 · `whir/catalog-info.yaml` — the dependency graph

Backstage `catalog-info` shape, so you can likely reuse what you already have.
Only `metadata.name` and `spec.dependsOn` are read; everything else is discarded
at parse time.

Edges are one-directional: a service declares what it depends on, never who
depends on it. Nothing here needs to be complete — it needs to be *true*. The
graph is used to decide whether a piece of evidence is topologically coherent
(§3.4), so a fabricated edge is worse than a missing one.

### 3.3 · `whir/state-queries.yaml` — is this dependency healthy right now?

One instant PromQL per dependency.

```yaml
version: v1
queries:
  - dependency: acme-db
    query: 'max(up{job="acme-db"})'
```

Value > 0 is healthy, 0 is degraded, and **no result — or any error — is
`unknown`**. That's a state, not an error, because "we couldn't tell" and "not
affected" must never look alike.

Aggregate with `max()` or `count()`. A double-scraped target can return more than
one series, and aggregating here means nothing downstream ever has to pick which
series to believe.

### 3.4 · `whir/evidence-queries.yaml` — what the reasoner may cite

The read-only PromQL the `metrics` tool exposes. The model calls a query **by
name** and cites results by name; it never sees a raw series, only a one-line
digest. There is no path by which raw telemetry enters the reasoning
conversation.

```yaml
version: v1
queries:
  - name: acme_api_error_ratio
    query: 'sum(rate(acme_api_requests_total{status=~"5.."}[5m])) / sum(rate(acme_api_requests_total[5m]))'
    subject: acme-api
```

`subject` names the topology entity a result is *about*, and it is load-bearing.
The readiness gate requires at least one **live, topologically coherent**
citation before a proposal may be emitted — coherent meaning the subject is
either the affected service itself or a node in the frozen topology snapshot. A
live metric about something the signal has no declared relationship to may
corroborate a hypothesis, but it can't clear the gate alone.

Omitting `subject` makes no topology claim at all, and the gate **fails closed**
on that: an untagged ref still enters the evidence list and can corroborate a
hypothesis something in-topology already grounds, but it can never be the sole
citation that clears the gate. A query you forgot to tag shows up as a
suppressed set with `reason: "evidence"` in the audit trail. Leave `subject` off
only where a query honestly spans several nodes — a cluster-wide aggregate, a
count across namespaces.

The same file tells the `loki` and `kube` tools which node *their* citations are
about. They take free-form coordinates from the model rather than named queries,
so the tag is resolved from the coordinates instead:

```yaml
subjects:
  - subject: acme-api
    namespace: acme
```

A rule constrains only the coordinates it names, and matches when all of them
agree; the most specific match wins, and two equally specific rules naming
different subjects resolve to nothing. `acme` owns its whole namespace, so no
labels are needed. A namespace holding five services needs one rule per service —
otherwise a query across all of them is evidence about none of them, and it will
say so by grounding nothing.

The same rules also place *changes*. When ArgoCD reports a synced object, clank
resolves it here to learn which node changed, and the coordinates it has are a
namespace, a kind and a name — never labels, which ArgoCD's resource inventory
doesn't publish:

```yaml
subjects:
  - subject: cephblockpool
    namespace: rook-ceph
    kind: CephBlockPool
```

Key those on `kind` where a kind identifies a node by itself. Object names drift
between clusters — the block pool is `replicapool` on three of our rigs — and the
kind doesn't. If every rule you author names labels, evidence resolves and change
never does: the events still arrive, they just carry Kubernetes names your
`catalog-info.yaml` has never heard of, so nothing joins and the causal term
contributes nothing to any confidence. That failure is silent from inside the
loop, which is why `TestShippedEvidenceConfigs_CarryRulesAChangedResourceCanMatch`
fails the build for a rig whose rules are all label-constrained.

**Author at least two backends.** The grounding tier counts *distinct backends*,
not citations, so a proposal cited from three Prometheus queries is corroborated
once. Metrics alone tops out one tier below where most authored floors sit; if
your policy floor is 0.75 and your only backend is Prometheus, every proposal
escalates.

> **Verify every metric name against your actual Prometheus before trusting this
> file.** A query that returns nothing is indistinguishable, from inside the
> reason loop, from a system that's fine. The same goes for `subjects`: those
> namespaces and labels are facts about your cluster, and `kubectl get pods -n
> <ns> --show-labels` is how you check them.

### 3.5 · `actions/failure-classes.yaml` — what a class means for you

The six class identifiers are the engine's reasoning vocabulary:

`service_failure` · `dependency_saturation` · `resource_exhaustion` ·
`redundancy_degraded` · `traffic_shift` · `unknown`

**You author what each one means in your domain and which actions serve it — not
a seventh class.** Every identifier needs a non-empty description. A class the
model can name but was never given the meaning of is a class it will guess at.

**This file is global, shared across every onboarded domain — not yours to
author from a blank slate.** A class's description has to stay true for every
action filed under it, in every domain, not just the one you're adding. Read
the existing description before you widen it; if your new action's fix shape
doesn't match what's already written there, either the description needs
broadening (name both fix shapes explicitly) or your action belongs under a
different class. A description that quietly contradicts one of its own
actions is exactly the mislabel risk the discriminator note below warns
about, just introduced by the file being edited instead of by the model.

Write the *discriminator* explicitly. Not "the dependency is saturated" but "cite
the dependency's own saturation evidence, not just elevated error ratio on the
API." That second sentence is what stops a plausible mislabel, and a mislabel is
how a real action gets proposed for the wrong reason.

Keep `unknown` honest: declining is a first-class outcome, and the description
should say so. Otherwise the model reads "an action exists for this class" as a
reason to pick it.

### 3.6 · `actions/catalog.yaml` — the autonomy boundary

**This file is the catalog.** There is no Go copy behind it. An action added here
is proposable and executable with no Go edit; an action deleted here can no
longer be proposed or executed by anything.

```yaml
- name: acme-shed-load
  applicableFailureClasses: [service_failure]
  applicableTiers: [tier-1]
  action:
    description: >-
      Shed load by scaling acme-api down to its minimum serving replicas,
      trading throughput for stability while the fault clears; reversible.
    scopeParameters:
      serving_replicas: {min: 1, max: 10, default: 2}
  blastTier: low
  reversal:
    method: restore-acme-capacity
    fallback: page-oncall
    restoreOnSuccess: false
  execution:
    forward: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 2}]
    reverse: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 10}]
  successCriteria:
    metric: acme_api_error_ratio
    target: "acme_api_error_ratio < 0.01"
    window: 300000000000 # 5 minutes, in nanoseconds
    severityQuery: acme_api_error_ratio
    severityReductionPct: 0.8
```

Field by field, on the parts that aren't obvious:

- **`description`** is read by the model. It's the only place the reasoner learns
  what this action *does* and what it trades. Write the tradeoff, not just the
  mechanism.
- **`blastTier`** (`low`/`med`/`high`) is authored by a human, never computed by
  the reasoner. With reversibility it decides the risk band: reversible and
  low/med fires unattended; reversible but `high`, or anything with no reversal,
  is held for a human.
- **`scopeParameters`** bound magnitude. The model picks *which* action; it does
  not get to decide that this incident's throttle is 73%. The rendered order uses
  the authored `default`.
- **`reversal.method`** is the audit *label* for the undo — what an operator and
  the trail call it. The mutation itself is `execution.reverse`. Nothing checks
  that the two describe each other, so keep them honest by hand.
- **`reversal.restoreOnSuccess`** declares the forward mutation **temporary**: a
  *met* success window still runs `execution.reverse`. Default `false` leaves a
  succeeding action applied, which is what a flag flip or a config fix wants.
  Set `true` for a tuning knob whose authored value is the steady state. Get this
  wrong in the `true` direction and you undo a fix; wrong in the `false`
  direction and you leave a temporary change applied forever with nothing
  watching it.
- **`severityReductionPct`** is your *forecast* of how much this action cuts
  severity, and it's what the outcome gets scored against. **Author it
  honestly.** A plausible-but-ineffective action with a truthful low number is
  exactly how the ranker learns to prefer the one that works. Omit it entirely
  and the forecast is `nil` — *unforecast*, which is not the same as a forecast
  of zero.
- **`window`** is a Go duration in nanoseconds. `300000000000` is five minutes.
  It's how long the convergence watcher waits before deciding whether this
  worked, so it should be longer than the change plausibly takes to show up in
  `metric`.

### 3.7 · `hiss/policy.yaml` — governance, and the only place a threshold lives

```yaml
version: acme-v1
floors:
  tier-1:
    service_failure: 0.75
maxBand:
  tier-1: act_reversible
autoBand:
  tier-1: act_reversible
requireReversal: true
freezeWindows: []
```

- **`floors`** is tier → failure class → minimum confidence. **Every actuatable
  (tier, class) pair needs an entry.** A pair with no entry clears the confidence
  veto on *any* nonzero confidence at all, which quietly substitutes the
  reasoner's judgment for a real minimum. `TestPolicy_FloorsCoverEveryActuatableClass`
  enforces this against the shipped catalog; write the equivalent for yours.
- **`maxBand`** caps the authority an action may *request*. **`autoBand`** caps
  the *computed* risk that may fire unattended. Past `autoBand` the action is
  **held** — approved in principle, waiting on a human ack.
  **Both need an entry per tier.** A tier absent from either map gets an
  unrecognized band value, which ranks *above* every real band — so the ceiling
  can never fire and that tier never holds. Absent is not restrictive here; it's
  the opposite. (Absence *on a candidate* is read the safe way — no governance
  level means it requests `observe`, the lowest band. It's the policy side that
  needs to be complete.)
- **`requireReversal: true`** escalates anything with no reversal path rather
  than running it.
- **`freezeWindows`** are named time ranges during which nothing is approved.
- **`version`** is stamped onto every decision, so the trail can always answer
  "governed under which rules?" Bump it when you change this file.

**Start conservative.** High floors and `autoBand: observe` mean everything gets
held for a human — which is a perfectly good first week. You can watch the whole
pipeline reason correctly and hold, and loosen from evidence rather than from
hope.

---

## 4 · The execution verbs

An action's `execution` block names a **bounded mechanism the actuator already
compiles** and points it at your resources. Config *picks* a mechanism; it never
*describes* a new one.

| Verb | Required targets | What it does |
|---|---|---|
| `scale` | `namespace`, `deployment`, `replicas` | sets replica count (`replicas: 0` is valid and distinct from omitted) |
| `restart` | `namespace`, `deployment` | rolls the pods — the same mechanism as `kubectl rollout restart` |
| `flagVariant` | `namespace`, `configMap`, `dataKey`, `flag`, `variant` | flips a feature-flag variant inside a ConfigMap |
| `exec` | `namespace`, `selector`, `command` | runs argv in a matching pod |

Both `forward` and `reverse` are required, and either may be a **list of steps
run in order**, stopping at the first failure. **An irreversible action cannot be
authored** — the actuator refuses to bind a contract with no reverse.

Order your steps deliberately. If a forward pauses something that owns a value,
the reverse should restore the value *first* and unpause *last*, so the thing you
unpaused comes back to find the world already correct.

Validation happens at **startup**, not on first approval: an unknown verb, a
missing target, or a forward step with no reverse refuses to load. The process
fails to start and names the contract and the step.

> ⚠️ **`exec` takes argv, so this file is an execution surface.** Whoever can
> merge the catalog can run a command in any pod thump's ServiceAccount can
> reach. What bounds it is RBAC (`pods/exec`, scoped per namespace), the kill
> switch, and hiss's policy — **not** the verb list. Scope that ServiceAccount
> tightly, and review catalog changes the way you'd review a change to an
> executor. See `CONTRIBUTING.md`.

**When you need a verb that isn't here** — a genuinely new *kind* of mutation —
that's a new mechanism in `internal/actuate` plus a test. Roughly 5% of
onboarding work is expected to land there, and it's the autonomy boundary earning
its keep: the bounded vocabulary is *why* the catalog can be trusted as the
blast-radius bound. It's also a welcome contribution.

---

## 5 · Wiring it up

Each beat reads a typed, validated environment once at startup and reports
**every** missing required variable together, so a bad deploy fails with a
complete list instead of one redeploy per variable discovered.

Two transport modes. With `NATS_URL` set, the beats speak JetStream and persist
to an S3-offloaded WAL. Without it, they poll directories — which is how the
pipeline is exercised end to end with no infrastructure at all.

**Shared:**

| Variable | Notes |
|---|---|
| `NATS_URL` | set → broker mode; unset → offline directory mode |
| `PROM_URL` | Prometheus. Required by rattle; optional elsewhere, where empty disables the tool that needs it |
| `WAL_DIR`, `WAL_CONFIG`, `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `TLS_CERT_FILE`, `TLS_KEY_FILE`, `TLS_CA_FILE`, `THUMP_SEAL_KEY` | required in broker mode only |

**Per beat:**

| Beat | Required | Notable optional |
|---|---|---|
| `rattle` | `PROM_URL`, `RATTLE_WATCH`, `RATTLE_QUERY_CONFIG` | `WHIR_CATALOG` + `WHIR_STATE_QUERIES`, `RATTLE_TRAFFIC`, `RATTLE_OUTBOX` |
| `clank` | `ANTHROPIC_API_KEY`, `ACTION_CATALOG`, `FAILURE_CLASSES`, `CLANK_WEIGHTS`, `CLANK_LIMITS` | `EVIDENCE_QUERIES`, `LOKI_URL`, `WHIR_*`, `DEDUPE_WINDOW` (default `1h`), `CLANK_TRANSCRIPTS` |
| `hiss` | `HISS_POLICY` | `APPROVALREQUESTS_ENABLED`, `APPROVALREQUEST_RETENTION` (default `24h`) |
| `thump` | `ACTION_CATALOG` | `THUMP_EXECUTOR` (default `dry`), `THUMP_KILLSWITCH`, `EVIDENCE_QUERIES`, `SLACK_WEBHOOK_URL` |

In offline mode each beat additionally requires its own inbox/outbox pair
(`CLANK_INBOX`, `HISS_OUTBOX`, and so on). `internal/config/config.go` is the
complete, authoritative list — read it before arming anything for real.

Two defaults worth internalizing. `THUMP_EXECUTOR` is `dry` unless you explicitly
set `live`. `DEDUPE_WINDOW` is an hour: a still-firing signal will not trigger a
fresh action on top of one already in flight.

---

## 6 · Checking your work

```sh
task ci
```

The guards that will catch an onboarding mistake, and what each one means:

| Failure | What you did |
|---|---|
| `contract names no executable mechanism` | unknown verb, missing target, or no `reverse` |
| `TestShippedCatalog_EveryContractIsWellFormed` red | a typo that unmarshals clean and leaves the action unreachable — bad failure class, bad blast tier, missing reversal |
| `TestPolicy_FloorsCoverEveryActuatableClass` red | an actuatable (tier, class) pair with no confidence floor |
| `watch file declares zero SLOs` | the watch list didn't parse the way you think it did |

That last category is the one to watch generally. **A typo in these files often
fails silently.** An unrecognized failure class or blast tier unmarshals to a zero
value with no error, and the action simply never gets proposed — you get a
system that looks like it's working and never acts. That's why the guards above
assert *reachability* rather than just parseability, and why a new domain should
get its own equivalent of the acme test. Copying
`test/onboarding/onboard_test.go` and pointing it at your fixture directory is
the cheapest version of that.

---

## 7 · Dry run, then live

**Dry run is the default and you have to opt into anything else.** In dry mode,
every approved decision is rendered in full — the concrete mutation, its targets,
its success criteria, its reversal — and nothing is touched. The outcome records
`{mode: dry_run, result: rendered}`.

Run it that way first, against real signals, until you've read enough rendered
orders to believe them.

**Going live needs two independent things:**

1. `THUMP_EXECUTOR=live`, and
2. an **armed kill switch** — a file at `THUMP_KILLSWITCH` containing
   `armed: true`.

The switch fails closed in every ambiguous case. A missing file, an unreadable
one, malformed contents: all leave live actuation off, and a failed reload
*clears* a previously armed state rather than latching it. A blocked order is
recorded as `{mode: live, result: blocked}` — a refusal is loud, never a silent
skip.

One deliberate exemption: a **reversal** order is not blocked by a disarmed
switch. Blocking cleanup mid-flight would strand infrastructure half-changed,
which is worse than letting one bounded, already-approved undo finish.

**Watching it work.** `trim incidents` folds the stream into a read-only view of
what the engine has done and what it's holding. When something is held for a
human, `trim approve <fingerprint>` emits an ack — and *hiss* re-issues the
approved decision. The operator surface never writes a decision itself, which is
why approving through it is still governance happening exactly once, in the
governor.

Deployment against a live cluster is scripted with Tilt — see `README.md`
§ *Standing it up locally*.
