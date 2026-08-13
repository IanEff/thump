# The local dev environment

A k3d cluster and a Tiltfile substrate that stand up everything `deploy/chart/thump`
assumes already exists, so the whole five-beat loop runs on a laptop with no rig repo,
no IAP tunnel, and no Ceph. Every other cluster profile (`ceph-lab`, `rook-gke`,
`rook-gce-k3s`, `thump-test`) is provisioned by a separate repo under `~/projects/ceph/`;
`dev` is provisioned by this one, via `deploy/dev/`.

One thing it does not do: it does not run the `acme` domain yet — `domains.acme.enabled` is `false`,
though the config and RBAC gates are already in place for when it lands.

## Prerequisites

- `k3d`, `helm`, `tilt`, `kubectl` — `brew install k3d helm tilt-dev/tap/tilt kubectl`
- Docker (or OrbStack) with **at least 12 GB RAM / 6 CPU** allocated to the VM. The
  substrate (Cilium, cert-manager, Prometheus, Alertmanager, Loki, Tempo, otel-collector,
  Grafana, S3Mock) runs about 5 GB; the trimmed OTel demo adds roughly another 4 GB.
- An Anthropic API key. clank has no fake `Model` implementation in this repo —
  `internal/replay` only replays one recorded transcript, it can't serve a live loop —
  so `ANTHROPIC_API_KEY` in `.env` is the one thing this environment can't stand up for
  you. A five-beat cycle on Haiku costs fractions of a cent; nothing about running this
  environment burns meaningful spend.

## Bringing it up

```sh
task dev:up
```

This is `task dev:cluster` (idempotent `k3d cluster create --config deploy/dev/k3d.yaml`)
followed by `tilt up -- --cluster=dev`. Cluster creation can't live inside the Tiltfile
itself — `allow_k8s_contexts()` and `helm()` both evaluate at Tiltfile load, so the k3d
context has to exist before Tilt starts at all.

Once Tilt is running, its `dev-substrate` resource runs `deploy/dev/bootstrap.sh`: ten
pinned Helm releases plus a handful of manifests, in an order that isn't arbitrary —
Cilium's own chart refuses to render a ServiceMonitor without `prometheus-operator-crds`
already installed, and `cluster-issuer.yaml` needs cert-manager's CRDs `Established`
before it can apply. Expect **10–15 minutes** on first boot; every step after is
`helm upgrade --install --wait`, so a re-run converges rather than reinstalling.

Every beat runs with `TRIGGER_MODE_MANUAL` in Tilt, same as every other profile — nothing
redeploys until you tell it to.

## Reaching the UI

No Gateway API or Ingress in this environment (`deploy/dev/values/cilium.yaml` turns
`gatewayAPI` off — see that file for why). Tilt runs each of the following as its own
supervised `kubectl port-forward`:

| Service | Local port | What it's for |
|---|---|---|
| Grafana | `localhost:3000` | Dashboards over the Prometheus + Tempo datasources |
| Prometheus | `localhost:9090` | Raw PromQL, including `slo:current_burn_rate:ratio` |
| Hubble UI | `localhost:12000` | Cilium's flow visibility |
| OTel demo frontend | `localhost:8080` | The Astronomy Shop storefront itself |

Grafana's admin password is `admin` (`deploy/dev/values/grafana.yaml`) — this cluster
never leaves your laptop, so it isn't worth managing as a secret.

## Breaking something

```sh
task chaos:cart-failure    # flip cartFailure on in otel-demo/flagd-config
```

This is `chaos/flagd-cart-failure.sh inject`, unchanged from every other profile — it
patches a ConfigMap, and `deploy/dev/values/otel-demo.yaml` carries the same
`mountedConfigMaps`/`null`-field rewiring the rig uses so flagd hot-reloads the change in
place instead of needing a restart.

Watch it land:

```sh
kubectl logs -n thump -l app.kubernetes.io/component=rattle -f
```

rattle's burn-rate detector scores a trailing window, so expect the first `"detection"`
log line something like 5–6 minutes after injection, not immediately. clank reasons over
it next (`"reasoned"`), hiss rules on the proposal (`"decision"`), and thump executes the
chosen action (patching `otel-demo/flagd-config`). Restore the flag when you're done:

```sh
task chaos:cart-restore
```

## Live mode and forge binding

`deploy/tilt-values-dev.yaml` sets `thump.executor: live` with `killSwitch.armed: true`.

Unlike the production rigs (`thump-test`, `rook-gce-k3s`), `dev` requires no GitOps target (`FORGE_REPO`). `actuate.New` (`internal/actuate/kube.go`) only requires a forge when the loaded catalog authors a `maintenanceRelease` action. The dev profile catalog (`config/dev/actions/catalog.yaml`) authors in-cluster mutations — patching `otel-demo/flagd-config` — and leaves release contracts to the rigs. `bind` validates every contract at startup, finds no release actions, and starts clean with `FORGE_REPO` unset.

When `task chaos:cart-failure` fires, the loop runs end-to-end through detection, evidence gathering, governance, and actual cluster mutation.

### Operator surface

When a detection requires manual intervention or governance holds an action, the operator CLI (`calipers`) can interact with the live cluster over the port-forwarded NATS server:

```sh
task dev:certs                          # extract NATS TLS certificates to bin/certs/
task dev:incidents                      # list active incidents over NATS
task dev:approve FP=<fingerprint>       # approve a held incident by fingerprint
```


## What's staged for later, not built

The `acme` synthetic domain — `docs/onboarding.md`'s own onboarding fixture — is
deliberately not wired into this environment yet. `domains.acme.enabled` is `false`,
but the RBAC gate, the config (`test/onboarding/testdata/acme/`), and the SLO rule
groups it needs (`deploy/dev/manifests/slo-rules.yaml`'s header comment names the exact
line range to pull from) are all already in place. Flipping it on is additive, not a
rework.
