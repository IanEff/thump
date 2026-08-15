# chaos — breaking the rig on purpose

This directory is how the engine gets tested against something that can actually hurt. Every
file here either injects a real fault into a live cluster or puts it back. There is no
simulation layer: a scenario scales a real Rook operator to zero, flips a real feature flag,
or kills a real OSD, and then waits to see whether thump notices, reasons, is permitted, and
acts.

**These scripts mutate a live cluster.** Read the one you're about to run.

## What this works on

The Ceph-specific fault mechanisms and `scenarios.yaml` itself are authored against exactly
one rig — **`thump-test`**, a four-node kind cluster running Rook-Ceph, ArgoCD, chaos-mesh,
and the OpenTelemetry demo. It's public:
[github.com/IanEff/thump-test](https://github.com/IanEff/thump-test). Nothing in that half
of the directory will do anything useful against a cluster that isn't shaped like it, and the
coupling is specific rather than vague:

`dev` (`k3d-thump-dev`, `docs/dev-environment.md`) is now a first-class second profile —
`flagd-cart-failure.sh` and `acme-fault.sh` work unchanged against it, since both just patch
an otel-demo/acme ConfigMap key with no rig-shape assumption baked in. Confirmed live
2026-08-15: `task chaos:cart-failure` against dev drove a clean autonomous resolution
(`thump-running-notes.md`). `scenarios.yaml` itself hasn't followed — it's still pinned to
`rig: thump-test` file-wide (see below).

- `scenarios.yaml` names its rig on line 10 (`rig: thump-test`), and every row's `signalRef`
  has to be a fingerprint that rig's `config/thump-test/rattle/watch.yaml` can actually
  emit. `slo_burn:cephblockpool` and `slo_burn:cart` are watched objects in that file. Point
  the table at a rig without them and the fault fires, the settle window runs out, and you
  get a timeout indistinguishable from a rig that genuinely never settled.
- The preconditions patch ArgoCD Applications by name (`rook-operator` and five siblings)
  and exec into `deploy/rook-ceph-tools` in namespace `rook-ceph`.
- `flagd-cart-failure.sh` patches one key of `flagd-config` in namespace `otel-demo`.

The fingerprint half of that is enforced offline, not by convention:
`TestScenarios_WaitOnFingerprintsTheRigsWatchListCanActuallyProduce`
(`internal/harvest/scenario_test.go`) loads the table, loads the named rig's watch list, and
fails if any row waits on something no detector emits. The rig's name lives in the YAML for
exactly that reason — so the join can be checked without any rig's name being compiled into
the harvest package.

The other half — Application names, namespaces, tool pods — has no such guard. Those you
port by hand.

## The scenario table

`scenarios.yaml` is the only file here that a program reads. Each row is one calibration
datum's worth of work — what to break, what has to be true first, what the engine should
conclude, how long to let it settle, and what puts the rig back. **A row with no restore is
rejected at load**; a harvest that cannot restore is a rig teardown with extra steps.

Two rows ship today, deliberately spanning two unrelated domains:

| Row | Domain | Breaks | Expected verdict |
|---|---|---|---|
| `osd-down-accelerate` | ceph | one OSD pod, with `mon_osd_down_out_interval` cut to 60s so degradation lands inside the run | `held` — `accelerate-recovery` is authored `blastTier: high`, so a human blesses it |
| `cart-failure` | otel-demo | the `cartFailure` flag in otel-demo's `flagd-config` | `approved` — `disable-cart-failure` is reversible, so it auto-approves |

They share no failure class (`redundancy_degraded` vs `service_failure`), and that's the
point: a fault injected in one domain must never be catalog-matched by an action authored
for the other. It's the test that the engine carries no domain knowledge.

Run one with the operator CLI, which applies the preconditions, fires the fault, watches for
a terminal outcome, and restores everything on the way out — including on Ctrl-C:

```
calipers harvest --row cart-failure --nats-url tls://nats.thump.svc:4222 \
  --tls-cert … --tls-key … --tls-ca … --kube-context k3d-thump-dev
```

`--kube-context` is optional but strongly recommended — without it, harvest fires against
whatever kubectl context happens to be current, and `scenarios.yaml`'s own `rig:` field is
checked by an offline test only, not by harvest itself.

## `preflight.sh`

Run before any scenario. It checks the three namespaces exist (Tilt garbage-collects the
`thump` namespace, and a green Tilt is not proof the objects are there), reports whether the
running beat has `THUMP_EXECUTOR` set to `live` or `dry-run`, and disables ArgoCD self-heal
on the six Applications a scenario might fight with.

That last one is not hygiene. ArgoCD reverts a scaled-to-zero operator in about a second, so
any action that pauses a reconciler doesn't hold without it — a limitation of the action, not
of the script, and one the catalog cannot warn a stranger about. See `docs/design-decisions.md`
D-10.

## The fault mechanisms

**chaos-mesh YAML**, applied with `kubectl apply -f`: `osd-pod-failure*.yaml`,
`osd-chaos.yaml`, `osd-latency.yaml`, `rgw-network-{delay,loss}.yaml`,
`rgw-client-delay.yaml`, `stress-rgw.yaml`. Duration and mark-out timing are tuned per
action — `osd-pod-failure-autonomous.yaml` (480s/300s) exercises `hold-rebalance`, while
`osd-pod-failure-accelerate.yaml` (600s/60s) exercises `accelerate-recovery`.

**Shell scripts**, run directly: `flagd-cart-failure.sh inject|restore` patches one key of
otel-demo's flag ConfigMap. `pg-num-starve.sh`, `rgw-ratelimit-starve.sh`, and
`rgw-user-suspend.sh` route Ceph-side faults through `kubectl exec -n rook-ceph
deploy/rook-ceph-tools -- ceph …` — there is no `ceph` binary on the runner's PATH, and a
bare `ceph` call in a precondition exits 127.

## Two things that cost real time

**Detection lag is minutes, not seconds.** A fault that self-heals inside that window is
invisible to the engine, and that looks identical to a broken pipeline while being nothing of
the sort. Size the fault's duration against the lag before concluding anything.

**An action reporting `applied` has not necessarily changed anything.** thump does not read
values back after a mutation. `accelerate-recovery` shipped as an exit-0 no-op for a full
quarter because Rook has defaulted to `osd_op_queue=mclock_scheduler` since Quincy, which
ignores both recovery settings unless an override is set first. Check the value by hand
mid-flight.
