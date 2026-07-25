# Onboarding a domain to thump

> ⚠️ **Provisional.** Written alongside the port of the other `docs/` pages and
> not yet rewritten for outside readers — it assumes you've read `README.md`
> § Onboard your own domain, and it hasn't been walked by anyone but the author.
> If a step here doesn't work, that's a bug worth reporting rather than something
> you're doing wrong.

**The claim:** making thump watch, reason about, govern, and remediate a new
system takes no Go. Seven authored YAML files are the whole surface.

**The proof:** `test/onboarding/onboard_test.go`'s
`TestOperator_OnboardsANewDomainInConfigAlone` onboards a synthetic service
called `acme` from `test/onboarding/testdata/acme/` and drives it through every
beat — signal, reasoning, governance, execution — in `task ci`, with no API key
and no cluster. Copy that directory as your starting point, and read it with:

```sh
go test ./test/onboarding -v
```

The fixture domain is deliberately named `acme` and is deliberately not Ceph,
OpenTelemetry, flagd, or anything else the engine actually ships config for. It's
a tripwire: the day onboarding needs a domain-specific branch in Go, that test is
where it surfaces.

---

## The seven files

Per-site config lives under `config/<site>/`; the action catalog, failure classes,
and governance policy are global (`config/actions/`, `config/hiss/`).

### 1 · `rattle/watch.yaml` — the steady-state contract

The SLOs to poll, and the dependencies whose health gates *trusting* a divergence
on this object. `contractRef` names the signal contract; dependency names must
match a `metadata.name` in your topology file exactly.

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

`role: blocking` means a degraded dependency attenuates confidence in this
signal — it lowers it, it never silently drops the signal.

### 2 · `whir/catalog-info.yaml` — the dependency graph

Backstage `catalog-info` shape; only `metadata.name` and `spec.dependsOn` are
read, everything else is discarded at parse time. Edges are one-directional: a
service declares what it depends on, never who depends on it.

### 3 · `whir/state-queries.yaml` — is this dependency healthy right now

One instant PromQL per dependency. Value > 0 is healthy, 0 is degraded, and no
result (or any error) is **unknown** — a state, not an error, because "we
couldn't tell" and "not affected" must never look alike.

```yaml
version: v1
queries:
  - dependency: acme-db
    query: 'max(up{job="acme-db"})'
```

Aggregate with `max()`/`count()`. A double-scraped target can return more than
one series, and aggregating here means nothing downstream ever has to pick which
series to believe.

### 4 · `whir/evidence-queries.yaml` — what the reasoner may cite

The read-only PromQL the `metrics` tool exposes. The model calls it by name and
cites results by name; it never sees a raw series, only a one-line digest.

```yaml
version: v1
queries:
  - name: acme_api_error_ratio
    query: 'sum(rate(acme_api_requests_total{status=~"5.."}[5m])) / sum(rate(acme_api_requests_total[5m]))'
    subject: acme-api
```

`subject` names the topology entity a result is *about*. It's load-bearing: a
citation whose subject is neither the signal's own service nor a node in the
frozen topology snapshot is incoherent and doesn't corroborate. Omitting the tag
makes no topology claim at all, which is different from making a false one.

**Verify every metric name against your actual Prometheus before trusting this
file.** A query that returns nothing is indistinguishable, from inside the reason
loop, from a system that's fine.

### 5 · `actions/failure-classes.yaml` — what a class means for you

The six class identifiers (`service_failure`, `dependency_saturation`,
`resource_exhaustion`, `redundancy_degraded`, `traffic_shift`, `unknown`) are the
engine's reasoning vocabulary — you author what each one *means* in your domain
and which actions serve it, not a seventh class.

Every identifier needs a non-empty description. A class the model can name but
was never given the meaning of is a class it will guess at. Write the
discriminator explicitly — "cite the dependency's saturation evidence, not just
elevated error ratio on the API" — because that's the sentence that stops a
plausible mislabel.

Keep `unknown` honest: declining is a first-class outcome, and the description
should say so, or the model treats "an action exists for this class" as a reason
to pick it.

### 6 · `actions/catalog.yaml` — the autonomy boundary

**This file is the catalog.** There's no Go copy behind it: an action added here
is proposable and executable with no Go edit, and an action deleted here can no
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

- **`blastTier`** (`low`/`med`/`high`) is authored by a human, never computed by
  the reasoner. It feeds the risk shaper, which decides how much latitude the
  action earns unattended.
- **`scopeParameters`** bound magnitude. The model picks *which* action; it does
  not get to decide this incident's throttle is 73%. The rendered order uses the
  authored `default`.
- **`reversal`** is the audit *label* for the undo — what an operator and the
  trail call it. The mutation itself is `execution.reverse`. Nothing checks the
  two describe each other, so keep them honest by hand.
- **`severityReductionPct`** is your *forecast* of how much this action cuts
  severity, and it's what the outcome gets scored against. Author it honestly:
  a plausible-but-ineffective action with a truthful low number is exactly how
  the ranker learns to prefer the one that works. Omit it entirely and the
  forecast is `nil` — unforecast, which is not the same as a forecast of zero.
- **`window`** is a Go duration in nanoseconds. `300000000000` is five minutes.

### 7 · `hiss/policy.yaml` — governance, and the only place a threshold lives

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
```

- **`floors`** is tier → class → minimum confidence. **Every actuatable (tier,
  class) pair needs one**: a pair with no entry clears the confidence veto on any
  nonzero confidence at all, which quietly substitutes the reasoner's judgment
  for a real minimum. A test enforces this against the shipped catalog.
- **`maxBand`** caps the authority an action may *request*; **`autoBand`** caps
  the *computed* risk that may fire unattended. Past `autoBand` the action is
  held for a human — approved in principle, waiting on an ack.
- **`version`** is stamped onto every decision, so the trail can always answer
  "governed under which rules?"
- **`requireReversal: true`** escalates anything with no reversal path rather
  than running it.

---

## The execution verbs

`execution` names a **bounded mechanism the actuator already compiles** and points
it at your resources. Config *picks* a mechanism; it never *describes* a new one.

| Verb | Required targets | What it does |
|---|---|---|
| `scale` | `namespace`, `deployment`, `replicas` | sets replica count (`replicas: 0` is valid and distinct from omitted) |
| `restart` | `namespace`, `deployment` | rolls the pods, same mechanism as `kubectl rollout restart` |
| `flagVariant` | `namespace`, `configMap`, `dataKey`, `flag`, `variant` | flips a feature-flag variant in a ConfigMap |
| `exec` | `namespace`, `selector`, `command` | runs argv in a matching pod |

Both `forward` and `reverse` are required, and either may be a list of steps run
in order. **An irreversible action cannot be authored** — the actuator refuses to
bind a contract with no reverse.

Validation is at **startup**, not first approval: an unknown verb, a missing
target, or a forward step with no reverse refuses to load. The process fails to
start and says which contract and which step.

> ⚠️ **`exec` takes argv, so this file is an execution surface.** Whoever can
> merge the catalog can run a command in any pod thump's ServiceAccount can
> reach. What bounds it is RBAC (`pods/exec`, scoped per namespace), the kill
> switch, and hiss's policy — *not* the verb list. See `CONTRIBUTING.md`.

**When you need a verb that isn't here** — a genuinely new *kind* of mutation —
that's a new mechanism in `internal/actuate` plus a test. Roughly 5% of
onboarding work is expected to land here, and it's the autonomy boundary earning
its keep: the bounded vocabulary is *why* the catalog can be trusted as the
blast-radius bound.

---

## Checking your work

```sh
task ci
```

The guards that will catch an onboarding mistake, and what each one means:

| Failure | What you did |
|---|---|
| `contract names no executable mechanism` | unknown verb, missing target, or no `reverse` |
| `TestShippedCatalog_EveryContractIsWellFormed` red | a typo that unmarshals clean and leaves the action unreachable — bad class, bad blast tier, missing reversal |
| `TestPolicy_FloorsCoverEveryActuatableClass` red | an actuatable (tier, class) pair with no confidence floor |
| `watch file declares zero SLOs` | the watch list didn't parse the way you think it did |

That last category is the one to watch generally. **A typo in these files often
fails silently** — an unrecognized failure class or blast tier unmarshals to a
zero value with no error, and the action simply never gets proposed. That's why
the guards above assert *reachability* rather than just parseability, and why a
new domain should get its own equivalent of the acme test.

Dry-run is the default. Going live is a separate, deliberate opt-in and
additionally requires an armed kill switch — see `README.md` § Standing it up
locally.
