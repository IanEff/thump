# Outside knowledge — verified against docs.tilt.dev during authoring
# (2026-07-05), not carried over from prior training. See
# phase2-ws2-deploy-guide.md Stage 3 for the W0-1..W0-4 decisions this rests
# on, and thump-running-notes for the k3s-on-Lima registry plumbing this
# depends on.

# Multi-cluster dev loop (phase2-converge-rook-gke-guide.md Stage 0.6
# addendum): everything that differs between ceph-lab, rook-gke, and
# rook-gce-k3s — kubectl context, build platform, image registry, chart
# values overlay — lives in this one table, picked by
# `tilt up -- --cluster=<name>`.
#
# platform: None for ceph-lab (Lima's arm64 VMs match the Mac natively, no
# cross-compile needed); 'linux/amd64' for rook-gke and rook-gce-k3s (both
# are real GCE VMs — e2-standard-4 on rook-gke, e2-medium/e2-standard-2 on
# rook-gce-k3s — all amd64, and CGO_ENABLED=0 means buildx cross-compiles
# for free, no QEMU — same trick as the release Dockerfile's
# $BUILDPLATFORM/$TARGETARCH staging). Deliberately single-platform per
# profile, not multi-arch: only one context is ever live at a time, so
# building the arch you're not deploying to is pure waste (the guide's own
# ruling).
#
# registry: ceph-lab's insecure local registry lives on the Mac's
# socket_vmnet subnet (192.168.56.0/24) and is reachable from Lima nodes only
# — GKE's/GCE's nodes are on GCP's network and have no route to it at all.
# rook-gke and rook-gce-k3s both have to push somewhere their nodes can
# actually pull from, so both reuse ghcr.io/ianeff, the same registry the
# release path (`task images`) already pushes to.
#
# rook-gce-k3s's context is IAP-only — `just tunnel` (in
# ~/projects/ceph/rook-gce-k3s) must already be running before `tilt up`,
# same as any other kubectl access to this rig; Tilt has no hook to start
# that tunnel itself.
CLUSTERS = {
    "ceph-lab": {
        "context": "ceph-lab",
        "platform": None,
        "registry": "192.168.56.1:5005",
        "values": "deploy/tilt-values.yaml",
    },
    "rook-gke": {
        "context": "gke_terraform-sandbox-430820_us-central1_rook-ceph-gke",
        "platform": "linux/amd64",
        "registry": "ghcr.io/ianeff",
        "values": "deploy/tilt-values-rook-gke.yaml",
    },
    "rook-gce-k3s": {
        "context": "ceph-gce",
        "platform": "linux/amd64",
        "registry": "ghcr.io/ianeff",
        "values": "deploy/tilt-values-rook-gce-k3s.yaml",
    },
    # thump-test (Wave 7): the rook-gce-k3s rig's successor — same GCE/k3s/
    # Cilium substrate, same kubectl-context-is-the-cluster-name convention
    # (thump-test rig repo's `just credentials` writes it), Ceph KEPT and the
    # OTel demo added alongside it as a second, orthogonal domain. context is
    # IAP-only exactly like rook-gce-k3s — `gcloud compute start-iap-tunnel
    # thump-test-control-plane 6443 ...` (thump-test repo README/CLAUDE.md)
    # must already be running before `tilt up`, Tilt has no hook to start it.
    "thump-test": {
        "context": "thump-test",
        "platform": "linux/amd64",
        "registry": "ghcr.io/ianeff",
        "values": "deploy/tilt-values-thump-test.yaml",
    },
    # dev: docs/dev-environment.md — the fully-local profile. Every other
    # profile targets a cluster built by a separate rig repo; this one is
    # built by `task dev:cluster` (deploy/dev/k3d.yaml) and its substrate is
    # installed by this same Tiltfile (the dev-substrate local_resource,
    # below) rather than by out-of-band provisioning. platform: None — k3d's
    # node containers share the host kernel natively, no cross-compile
    # needed, same reasoning as ceph-lab. registry is NOT k3d-prefixed
    # (unlike the kubectl context) — confirmed live, see deploy/tilt-values-
    # dev.yaml's image.registry comment.
    "dev": {
        "context": "k3d-thump-dev",
        "platform": None,
        "registry": "thump-dev-registry:5050",
        "values": "deploy/tilt-values-dev.yaml",
    },
}

config.define_string(
    "cluster",
    usage="which CLUSTERS profile to target: ceph-lab (default), rook-gke, rook-gce-k3s, or thump-test",
)
cfg = config.parse()
cluster_name = cfg.get("cluster", "ceph-lab")
if cluster_name not in CLUSTERS:
    fail("unknown --cluster %s — must be one of: %s" % (cluster_name, ", ".join(CLUSTERS.keys())))
cluster = CLUSTERS[cluster_name]

# `ceph-lab` doesn't match any of Tilt's known-local context names (kind-*,
# k3d-*, minikube, docker-desktop, ...), so Tilt refuses to deploy to it by
# default as a safety rail against accidentally hitting a real cluster.
# Named explicitly (not k8s_context()) so a context typo fails loud instead
# of silently allow-listing whatever's currently active. Same rail now
# guards rook-gke too: allow-listing only the ONE profile you asked for on
# the CLI means picking the wrong live kubectx still fails loud, not silent.
allow_k8s_contexts(cluster["context"])

# thump-registry: k3s-on-Lima doesn't share the Mac's Docker daemon, and this
# isn't kind/k3d/minikube — none of Tilt's built-in cluster-loader shortcuts
# apply. But the Mac sits on the same socket_vmnet subnet as the lab
# (192.168.56.0/24, as 192.168.56.1), so a registry container published
# there is reachable from every Lima node. ceph-lab's common.sh trusts
# 192.168.56.1:5005 as an insecure registry when SANDBOX_DEV_REGISTRY_ENABLED=1
# (provisioning/scripts/common.sh in the ceph-lab repo) — that's the other
# half of this seam, out of this repo.
#
# cmd (not serve_cmd): this is a one-shot idempotent "ensure it exists" check,
# not a long-running process Tilt should supervise — --restart unless-stopped
# is what keeps it up across Mac/Orbstack restarts.
local_resource(
    "thump-registry",
    cmd="docker inspect thump-registry >/dev/null 2>&1 || "
    + "docker run -d --name thump-registry --restart unless-stopped "
    + "-p 5005:5000 registry:2",
    labels=["infra"],
)

# kubectl_local: every local_resource below drives kubectl against the target
# cluster, and on the GCE rigs that cluster's API server is reachable only
# through a single `gcloud compute start-iap-tunnel` relay on localhost:6443.
# That relay drops connections under the burst of concurrent requests a
# `tilt up` produces — surfacing as `net/http: TLS handshake timeout` or
# `unexpected EOF` from whichever kubectl happened to be in flight.
#
# Tilt never retries a failed local_resource. So a two-second blip leaves the
# resource red and every k8s resource that depends on it `pending` forever,
# with no failing pod and no rolling log to point at — the run reads as hung
# rather than failed, and the usual reflex (delete the namespace, re-trigger
# resources by name) rebuilds it half-applied. Wrapping the body here is what
# stops a transport hiccup from costing a whole session.
#
# Two guards, in order:
#   1. Wait for the API to answer before running the body at all, so an
#      unreachable cluster reports itself as one and names the tunnel.
#   2. Retry the body. A genuine config error (.env missing, key unset) still
#      fails on the first pass — those branches `exit 1`, which leaves the
#      shell outright and never reaches the next iteration.
def guarded(name, body):
    kubectl = "kubectl --context " + cluster["context"]
    if cluster_name == "dev":
        unreachable_hint = "is the k3d cluster up? (task dev:cluster)"
    else:
        unreachable_hint = "is the IAP tunnel up? (just tunnel, in the rig repo)"
    preflight = (
        "waited=0; "
        + "until "
        + kubectl
        + " get --raw /readyz >/dev/null 2>&1; do "
        + "waited=$((waited+2)); "
        + "if [ $waited -ge 60 ]; then "
        + 'echo "thump: API server for context '
        + cluster["context"]
        + " unreachable after ${waited}s — "
        + unreachable_hint
        + '" >&2; exit 1; '
        + "fi; sleep 2; done; "
    )
    return (
        "bash -c '"
        + "for attempt in 1 2 3; do "
        + preflight
        + "{ "
        + body
        + "; } && exit 0; "
        + 'echo "thump: '
        + name
        + ' attempt $attempt failed (transport?), retrying in 5s" >&2; '
        + "sleep 5; done; "
        + 'echo "thump: '
        + name
        + ' failed 3 times — this is a real error, not a blip" >&2; exit 1'
        + "'"
    )


def kubectl_local(name, body, labels=["infra"], resource_deps=[]):
    local_resource(name, cmd=guarded(name, body), labels=labels, resource_deps=resource_deps)


# ensure_ns_cmd: every secret resource needs the namespace to exist first, and
# create-namespace-if-absent is the same three lines each time.
def ensure_ns_cmd(name):
    return (
        "kubectl --context "
        + cluster["context"]
        + " create namespace "
        + name
        + " --dry-run=client -o yaml | kubectl --context "
        + cluster["context"]
        + " apply -f - >/dev/null"
    )


ENSURE_NS = ensure_ns_cmd("thump")

# dev-substrate (cluster=dev only): everything deploy/chart/thump assumes is
# already on the cluster — cert-manager+ClusterIssuer, prometheus-operator
# CRDs, kube-prometheus-stack, Cilium, Loki/Promtail, Tempo/otel-collector,
# MinIO, the SLO PrometheusRule, the OTel demo — on every other profile,
# provisioning that is a separate rig repo's job, done long before `tilt up`
# ever runs. This profile has no rig repo, so deploy/dev/bootstrap.sh does
# it here instead, as the one thing every other dev-cluster resource depends
# on transitively through nats/thump-s3-secret below.
#
# guarded() (not a plain local_resource cmd): bootstrap.sh's very first
# helm call needs a reachable API server exactly like every other
# kubectl/helm call in this file, and its ~10-15 minute runtime means a
# transient blip is expensive to just fail outright on — same reasoning as
# every other guarded() call here, scaled up.
if cluster_name == "dev":
    local_resource(
        "dev-substrate",
        cmd=guarded("dev-substrate", "./deploy/dev/bootstrap.sh"),
        labels=["substrate"],
    )

# thump-anthropic-secret: clank's Secret is meant to pre-exist out-of-band
# (the lab's SOPS flow owns it in prod — see deploy/chart/thump/templates/
# secret.yaml's comment). Under Tilt there's no SOPS flow, so this is the dev
# stand-in: read the key from a gitignored .env (ANTHROPIC_API_KEY="...")
# and apply the Secret directly via kubectl, never through
# anthropic.create/apiKey in a values file (that stays a `--set`-only
# escape hatch, per the chart's own warning against committing a plaintext
# key). --dry-run=client -o yaml | apply, not `create`, so re-running this
# after rotating the key in .env updates the Secret instead of no-op'ing.
kubectl_local(
    "thump-anthropic-secret",
    'set -a; source .env 2>/dev/null || { echo ".env not found at repo root — expected ANTHROPIC_API_KEY=\\"...\\""  >&2; exit 1; }; set +a; '
    + '[ -n "$ANTHROPIC_API_KEY" ] || { echo "ANTHROPIC_API_KEY not set in .env" >&2; exit 1; }; '
    + ENSURE_NS
    + " && kubectl --context "
    + cluster["context"]
    + " -n thump create secret generic thump-anthropic "
    + '--from-literal=api-key="$ANTHROPIC_API_KEY" --dry-run=client -o yaml | kubectl --context '
    + cluster["context"]
    + " apply -f -",
)

# thump-s3-secret: same posture as thump-anthropic-secret above — every beat
# (not just clank) needs this once NATS_URL is set, since every beat's
# broker path Require()s all four S3_* vars for its WAL shipper
# (internal/config/config.go). Sourced from .env, never from a values file,
# same reasoning as the Anthropic key. For rook-gce-k3s the four values come
# from ~/projects/ceph/rook-gce-k3s's storage.tf outputs (thump_s3_bucket /
# _endpoint / _access_key / _secret_key) — re-run `tofu output` and refresh
# .env after every fresh `tofu apply` there, since the bucket (and its HMAC
# key) gets torn down and recreated with `just destroy`/`just apply` same as
# everything else on that rig.
#
# dev is the one exception: there's no out-of-band bucket to source .env
# from, so this profile hardcodes the credentials adobe/s3mock accepts —
# it doesn't check them at all (see the s3mock k8s_resource below) — against
# the S3Mock instance this Tiltfile stands up directly, instead of requiring
# Stefan to hand-populate .env for a store this repo already provisions for
# him. ANTHROPIC_API_KEY above stays the one thing he does supply — that's
# the point of this env, not an oversight. endpoint is http:// against
# S3Mock's plain port 9090, not its bundled-self-signed-cert 9191:
# internal/config/config.go's RequireURL("S3_ENDPOINT", "http", "https")
# accepts either scheme for exactly this reason — declared plaintext for a
# vendored dev backend that doesn't serve real TLS, the same shape I-16
# already accepts for Prometheus/Loki (docs/invariants.md), rather than an
# unauthenticated TLS session dressed as a secure one. See the s3mock
# k8s_resource below for the port split.
if cluster_name == "dev":
    s3_body = (
        ENSURE_NS
        + " && kubectl --context "
        + cluster["context"]
        + " -n thump create secret generic thump-s3 "
        + '--from-literal=endpoint="http://s3mock.thump.svc.cluster.local:9090" --from-literal=bucket="thump-wal" '
        + '--from-literal=access-key="thump-dev" --from-literal=secret-key="thump-dev-secret" '
        + "--dry-run=client -o yaml | kubectl --context "
        + cluster["context"]
        + " apply -f -"
    )
    s3_deps = ["s3mock"]
else:
    s3_body = (
        'set -a; source .env 2>/dev/null || { echo ".env not found at repo root — expected S3_ENDPOINT/S3_BUCKET/S3_ACCESS_KEY/S3_SECRET_KEY" >&2; exit 1; }; set +a; '
        + "for v in S3_ENDPOINT S3_BUCKET S3_ACCESS_KEY S3_SECRET_KEY; do "
        + '  [ -n "${!v}" ] || { echo "$v not set in .env" >&2; exit 1; }; '
        + "done; "
        + ENSURE_NS
        + " && kubectl --context "
        + cluster["context"]
        + " -n thump create secret generic thump-s3 "
        + '--from-literal=endpoint="$S3_ENDPOINT" --from-literal=bucket="$S3_BUCKET" '
        + '--from-literal=access-key="$S3_ACCESS_KEY" --from-literal=secret-key="$S3_SECRET_KEY" '
        + "--dry-run=client -o yaml | kubectl --context "
        + cluster["context"]
        + " apply -f -"
    )
    s3_deps = []
kubectl_local("thump-s3-secret", s3_body, resource_deps=s3_deps)

# thump-seal-secret / thump-nats-js-key-secret: unlike thump-anthropic-secret
# and thump-s3-secret above, these two keys are pure internal material — no
# external system issues them, so there's nothing to source from .env. The
# chart's own secret.yaml self-provisions them on a real `helm install` via
# a `lookup`-guarded block, but `lookup` always reads empty under `helm
# template` (what Tilt's helm() runs) — the k8s_yaml() call above now
# filter_yaml()s both Secret objects out of the chart's rendered manifests
# entirely, so that inert guard never gets a chance to matter under Tilt.
# These local_resources are the *only* thing that ever creates or touches
# either secret in a Tilt session: create-if-absent, never touch an existing
# one (unlike thump-s3-secret's dry-run-apply, which is meant to re-sync
# every run) — `kubectl get ... || kubectl create ...`, not `--dry-run=client
# -o yaml | apply`.
#
# `get || create` is the obvious shape for create-if-absent and it is the wrong
# one here: a `get` that fails because the API was unreachable is
# indistinguishable from one that fails because the secret is absent, and the
# `||` branch answers both by minting a fresh random key. On thump-seal that
# orphans every sealed WAL segment; on nats-js-key it orphans the on-disk
# JetStream store, which is exactly the failure recorded on 2026-07-31. So:
# only a literal NotFound authorises a create. Any other error returns
# non-zero so kubectl_local retries it, and says out loud that it declined to
# mint a key rather than doing it quietly.
def ensure_key_secret(resource, secret):
    kubectl = "kubectl --context " + cluster["context"]
    kubectl_local(
        resource,
        ENSURE_NS
        + " && out=$("
        + kubectl
        + " -n thump get secret "
        + secret
        + ' 2>&1); if [ $? -eq 0 ]; then exit 0; fi; case "$out" in *NotFound*) '
        + kubectl
        + " -n thump create secret generic "
        + secret
        + ' --from-literal=key="$(openssl rand -base64 32)" ;; *) echo "thump: cannot tell whether secret '
        + secret
        + ' exists, refusing to mint a replacement key over data encrypted under the old one: $out" >&2; false ;; esac',
    )


ensure_key_secret("thump-seal-secret", "thump-seal")
ensure_key_secret("thump-nats-js-key-secret", "nats-js-key")

# The namespace is filtered out of the chart's manifests (see the filter_yaml
# block below for why), so something else has to create it — and it has to
# exist before Tilt applies a single namespaced object. local() runs during
# Tiltfile evaluation, ahead of every apply and every local_resource, which is
# the ordering guarantee a local_resource can't give: Tilt's `uncategorized`
# bucket isn't addressable by k8s_resource(), so it can't be told to wait.
# Idempotent, so a reload re-runs it as a no-op rather than a teardown.
local(guarded("thump-namespace", ENSURE_NS), quiet=True, echo_off=True)

# domains.{acme,otelDemo}.enabled (values.yaml's own comment: "off by default
# so ceph-lab/rook-gke/rook-gce-k3s still deploy clean") gates RBAC in
# rbac-acme-actuate.yaml / rbac-otel-demo-actuate.yaml that targets a fixed
# namespace outside this chart's own manifests — acme's and otel-demo's
# workloads are provisioned by the thump-test rig repo, not by this Tiltfile.
# On a freshly-created thump-test cluster that provisioning is a separate,
# unordered process: the namespace can still be missing when the RBAC applies.
#
# These run as local_resources, not blocking local() — a `local()` here would
# re-run on every Tiltfile reload (any chart/values edit) and a slow/missing
# namespace would raise a Starlark traceback that kills the whole `tilt up`.
# The acme-rbac / otel-demo-rbac k8s_resource() blocks below (after
# k8s_yaml(rendered)) pull the Role/RoleBinding pair out of Tilt's
# "uncategorized" bucket and gate them on these via resource_deps, so a
# missing namespace is a pending resource, not a batch-wide abort — observed
# live 2026-08-03: thump-acme-actuate:role failed on "namespaces \"acme\" not
# found" and took thump-nats:configmap and nats-tls:certificate down with it,
# despite neither having anything to do with acme.
domain_values = read_yaml(cluster["values"], default={})
domains = domain_values.get("domains", {})
if domains.get("acme", {}).get("enabled", False):
    kubectl_local("acme-namespace", ensure_ns_cmd("acme"))
if domains.get("otelDemo", {}).get("enabled", False):
    kubectl_local("otel-demo-namespace", ensure_ns_cmd("otel-demo"))

# serviceMonitor.enabled gates templates/servicemonitor.yaml, which targets
# monitoring.coreos.com/v1's ServiceMonitor kind — a CRD kube-prometheus-stack
# owns, provisioned by the thump-test rig repo's own scripts, not by this
# chart. Same race as the acme/otelDemo namespaces above, one CRD over: on a
# freshly-created cluster that provisioning is a separate, unordered process,
# the CRD can still be unregistered when the ServiceMonitor applies.
#
# Runs as a local_resource, gated the same way as the namespace guards above:
# the thump-servicemonitor k8s_resource() block below (after
# k8s_yaml(rendered)) pulls the ServiceMonitor object out of the uncategorized
# bucket and gives it resource_deps=["servicemonitor-crd"], so a slow CRD is a
# pending resource, not a Starlark traceback that kills the whole session.
# Observed live 2026-08-07: kube-prometheus-stack's CRDs landed ~2.5 minutes
# after tilt up's first apply — far longer than a blocking eval-time local()
# can afford to sit through on every reload.
def ensure_crd_cmd(name, timeout_s=60):
    kubectl = "kubectl --context " + cluster["context"]
    if cluster_name == "dev":
        missing_hint = "is dev-substrate (deploy/dev/bootstrap.sh) still installing kube-prometheus-stack?"
    else:
        missing_hint = "is the monitoring stack (kube-prometheus-stack) still installing? check the rig repo provisioning scripts"
    return (
        "waited=0; until "
        + kubectl
        + " get crd "
        + name
        + " >/dev/null 2>&1; do "
        + "waited=$((waited+5)); "
        + "if [ $waited -ge "
        + str(timeout_s)
        + " ]; then "
        + 'echo "thump: CRD '
        + name
        + " not found after ${waited}s — "
        + missing_hint
        + '" >&2; exit 1; '
        + "fi; sleep 5; done"
    )


if domain_values.get("serviceMonitor", {}).get("enabled", False):
    # dev: the CRD comes from dev-substrate's own kube-prometheus-stack
    # install (a Tilt-managed local_resource, not an out-of-band rig repo),
    # so without this dep the two race — bootstrap.sh's Helm install easily
    # outlasts the 60s CRD-wait budget below, and servicemonitor-crd loses
    # every time on a cold cluster.
    crd_deps = ["dev-substrate"] if cluster_name == "dev" else []
    kubectl_local(
        "servicemonitor-crd",
        ensure_crd_cmd("servicemonitors.monitoring.coreos.com"),
        resource_deps=crd_deps,
    )

DEV_REGISTRY = cluster["registry"]

# COMMIT is resolved once at Tiltfile load (not per-build), so it reflects
# the last commit, not uncommitted dirty-tree edits, and only refreshes on
# Tiltfile reload / the next `tilt up` — an accepted tradeoff for a fast
# edit loop here. DATE is deliberately left as the Dockerfile's "unknown"
# default under Tilt — a per-build wall-clock stamp isn't worth the noise.
COMMIT = str(local("git rev-parse --verify HEAD || echo none")).strip()

# Fast host-native compilation for local dev loop (Option 2).
#
# `go tool otelc go build` (not a bare `go build`), same entry point
# Taskfile.yaml's `build` task uses — the only supported one. otelc's own
# instrumentation packages never live in go.mod (upstream issue #585,
# .gitignore's comment, Taskfile.yaml's otelc:check-gomod); `otelc go build`
# pins them in transiently, builds, and unpins in one invocation. A
# standalone `otelc setup` beforehand (this file's prior approach) writes
# cmd/*/otelc.runtime.go without ever pinning go.mod to match it, so a
# plain `go build` right after can't resolve the packages it just
# generated imports for — reproduced live 2026-08-10, see
# thump-running-notes.
#
# `go tool otelc` itself, NOT `./otelc-native` below, is what `task build`
# runs (Taskfile.yaml) — that path never cross-compiles, so it never hits
# the bug this local()+direct-invocation dance works around.
#
# `go tool <name>` resolves and EXECUTES a binary honoring the ambient
# GOOS/GOARCH, same as `go run` — and just as broken cross-OS: GOOS=linux
# is hardcoded below for every profile here (containers are always Linux,
# whatever the host is), so `go tool otelc` tries to build ITSELF as a
# Linux binary and then exec it on this Darwin host, failing "exec format
# error". Reproduced live 2026-08-11 on the dev profile, but the bug is
# universal, not dev-specific — ceph-lab/rook-gke/rook-gce-k3s/thump-test
# hit the identical failure the moment this Tiltfile tries to build any
# beat for them, this just hadn't been re-verified live since `otelc setup`
# was swapped for `otelc go build` above. Building otelc once here, under
# the host's own native GOOS/GOARCH (no override), and invoking that binary
# directly per beat — bypassing `go tool`'s dispatch entirely — sidesteps
# it: the binary itself always runs host-native, and only the `go build` it
# execs internally (a plain subprocess inheriting the per-beat env below)
# ever sees GOOS=linux.
OTELC_NATIVE = "bin/dev/otelc-native"
local(
    "mkdir -p bin/dev && go build -o " + OTELC_NATIVE + " go.opentelemetry.io/otelc/tool/cmd/otelc",
    quiet=True,
    echo_off=True,
)

if cluster["platform"] == "linux/amd64":
    target_arch = "amd64"
    platform_flag = "--platform linux/amd64 "
else:
    target_arch = "arm64"
    platform_flag = ""

for beat in ["rattle", "clank", "hiss", "thump", "bootstrap"]:
    cmd = (
        "mkdir -p bin/dev && "
        + "CGO_ENABLED=0 GOOS=linux GOARCH=" + target_arch + " "
        + "./" + OTELC_NATIVE + " go build -ldflags '-s -w -X main.version=dev -X main.commit=" + COMMIT + "' "
        + "-o bin/dev/" + beat + " ./cmd/" + beat + " && "
        + "docker build " + platform_flag + "-f Dockerfile.dev --build-arg BEAT=" + beat + " -t $EXPECTED_REF bin/dev"
    )
    custom_build(
        DEV_REGISTRY + "/thump-" + beat,
        command=cmd,
        # .otelc-build is NOT a dep, deliberately: it's otelc's own transient
        # build-state directory (.gitignore's comment), written by the `go
        # build` step above as output — not read as a source input by
        # anything. Listing it here (an earlier version of this loop did)
        # makes custom_build watch its own output: every build writes to
        # .otelc-build, which Tilt sees as a changed dep and re-triggers
        # immediately — an infinite self-rebuild loop, observed live
        # 2026-08-11 as thump-bootstrap rebuilding continuously. The fix is
        # to never list a build's own output directory as one of its deps.
        #
        # go.mod/go.sum are ALSO not deps, for the same reason, one level
        # deeper: `otelc go build` (this loop's `cmd`, above) pins its
        # instrumentation packages into the module root's go.mod/go.sum,
        # builds, then unpins — a real write-then-restore on disk, not
        # metadata. Confirmed live 2026-08-11 by reading otelc v1.0.1's own
        # source (tool/internal/setup/state.go's getBackupFiles, pin.go's
        # AutoPin): every `otelc go build` backs up and restores go.mod and
        # go.sum around the build, unconditionally. Watching them here means
        # every beat's build re-triggers itself — and since all 5 beats
        # share one module root, one beat's build re-triggers all 5,
        # compounding the loop instead of just repeating it. A real go.mod
        # edit (an actual `go get`) won't auto-rebuild under Tilt anymore;
        # `tilt trigger` after one is the manual escape hatch, worth it to
        # not live-loop the build on every single compile.
        deps=["cmd/" + beat, "internal"],
    )



# Unlike a bare `helm template` (what chart-lint runs, and why that path
# needs its own CRD-blind kubeconform pass), Tilt's built-in helm() always
# passes --include-crds, so crds/approvalrequest.yaml is already in this
# k8s_yaml() call — a separate manual k8s_yaml() of the same file duplicated
# the CRD and broke `tilt up` (2026-07-31). helm() watches the whole chart
# dir, so crds/ edits still live-reload same as templates/.
#
# namespace= is load-bearing, not decoration. Without it `helm template`
# resolves `.Release.Namespace` to "default", and three templates key off it:
# rbac-approver, rbac-hiss-approvalrequests, and secret.yaml. Every other
# template in the chart hardcodes `namespace: thump`, so 28 of 31 objects
# landed correctly and only the RBAC quietly went to the `default` namespace —
# where it grants nothing, since hiss's ServiceAccount lives in `thump`.
# `kubectl auth can-i create approvalrequests.thump.dev` answering `no` on a
# cluster whose CRD exists and whose Role exists (elsewhere) is the signature.
# Diagnosed 2026-07-31 after two sessions blamed it on Tilt re-triggering.
rendered = helm("deploy/chart/thump", namespace="thump", values=[cluster["values"]])

# secret.yaml's thump-seal and nats-js-key Secrets are guarded by
# `{{- if not (lookup ...) }}` so a real `helm install` mints them once and
# leaves them alone. `lookup` always reads empty under `helm template` (what
# helm() above runs), so left in this manifest set, that guard is inert:
# every Tiltfile reload re-renders a *freshly random* key and k8s_yaml
# applies it over whatever's already live. That's what broke thump-test
# 2026-07-31 — an unrelated chart edit (OTel env vars) triggered a reload
# mid-session, silently rotated nats-js-key's value, and the running NATS
# pod could no longer decrypt its own on-disk JetStream store ("unable to
# recover keys" / stream "could not be recovered"). Strip both Secret
# objects out of what k8s_yaml ever sees — thump-seal-secret and
# thump-nats-js-key-secret below (create-if-absent) become the *only* thing
# allowed to touch them under Tilt, enforcing the same once-only intent the
# chart's lookup guard has for a real `helm install`, by manifest filtering
# instead of a guard Tilt can't evaluate.
_, rendered = filter_yaml(rendered, kind="Secret", name="thump-seal")
_, rendered = filter_yaml(rendered, kind="Secret", name="nats-js-key")

# Namespace/thump comes out for the same reason, and it is the big one.
#
# The chart owns its own namespace (templates/namespace.yaml), correct for a
# real `helm install`. Under Tilt it means `Namespace/thump` is an object in
# the managed set, and Tilt garbage-collects the previous set on any reload
# that changes it. Deleting a Namespace is a *cascading* delete of everything
# inside it, so a one-line chart edit mid-session tears down ConfigMaps,
# Secrets, RBAC, and the `data-nats-0` PVC — and then Tilt re-applies into a
# namespace that is still Terminating, which the API server refuses with
# "unable to create new content in namespace thump because it is being
# terminated". Observed verbatim in Tilt's own log, 2026-07-31:
#
#     Beginning garbage collecting Kubernetes objects
#     Deleting kubernetes objects:
#     → Namespace/thump
#
# This is the mechanism behind three separate sessions' worth of symptoms that
# each got blamed on something else: the "clobbered" nats-js-key that left
# JetStream unable to decrypt its own store (the Secret was cascade-deleted
# with the namespace, then re-minted against a PVC that outlived it), the
# ConfigMaps and Roles "a per-resource trigger pass missed" (they were deleted,
# not skipped), and the namespace found stuck Terminating "for reasons not
# established". None of it needed a finalizer to explain it.
#
# ENSURE_NS in each local_resource above already creates the namespace
# create-if-absent, so nothing is lost by taking it away from Tilt — and once
# no Tilt operation can delete it, none of the above can recur.
_, rendered = filter_yaml(rendered, kind="Namespace", name="thump")
k8s_yaml(rendered)

# Pulls the acme/otel-demo RBAC and the ServiceMonitor out of Tilt's
# "uncategorized" bucket (see the acme-namespace/otel-demo-namespace/
# servicemonitor-crd comments above) so each can carry resource_deps on the
# local_resource that ensures its target namespace/CRD exists, instead of
# relying on Tilt's undocumented-as-guaranteed "local resources run first"
# default ordering. Guarded the same way as their local_resource — an
# objects= selector for an object absent from `rendered` fails outright.
if domains.get("acme", {}).get("enabled", False):
    k8s_resource(
        new_name="acme-rbac",
        objects=[
            "thump-acme-actuate:role", "thump-acme-actuate:rolebinding",
            "thump-acme-read:role", "thump-acme-read:rolebinding",
        ],
        resource_deps=["acme-namespace"],
        labels=["infra"],
    )
if domains.get("otelDemo", {}).get("enabled", False):
    k8s_resource(
        new_name="otel-demo-rbac",
        objects=[
            "thump-otel-demo-actuate:role", "thump-otel-demo-actuate:rolebinding",
            "thump-otel-demo-read:role", "thump-otel-demo-read:rolebinding",
        ],
        resource_deps=["otel-demo-namespace"],
        labels=["infra"],
    )
if domain_values.get("serviceMonitor", {}).get("enabled", False):
    k8s_resource(
        new_name="thump-servicemonitor",
        objects=["thump:servicemonitor:thump"],
        resource_deps=["servicemonitor-crd"],
        labels=["infra"],
    )

# Bring up NATS first — the beats dial it on boot; bring it up (and Ready) before them.
# On dev, nats-tls (certificates.yaml) can't issue until dev-substrate's
# cert-manager + thump-ca ClusterIssuer exist — without this dep, NATS's
# StatefulSet pod is stuck waiting on a Secret volume that never appears.
#
# objects=[...]: thump-nats:configmap and nats-tls:certificate have no owner
# reference to the nats StatefulSet Tilt can discover on its own, so without
# this they land in the "uncategorized" bucket alongside every other orphan
# object in the chart (RBAC, other Certificates, ...) — same bucket the
# acme-rbac/otel-demo-rbac/servicemonitor-crd comments above describe, and
# proven to bite this exact pair live 2026-08-11: an unrelated rook-ceph RBAC
# object failing on "namespace not found" took thump-nats:configmap and
# nats-tls:certificate down with it, permanently (the uncategorized bucket
# retries as one unit, so one object that can never succeed on this profile
# blocks every object sharing the bucket, forever — not just delayed).
# Folding both into the "nats" resource here means they apply exactly when
# the StatefulSet does, gated on the same nats_deps, immune to whatever else
# is or isn't wrong elsewhere in the uncategorized set.
nats_deps = ["thump-registry", "thump-nats-js-key-secret"]
if cluster_name == "dev":
    nats_deps.append("dev-substrate")
k8s_resource(
    "nats",
    objects=["thump-nats:configmap", "nats-tls:certificate"],
    labels=["broker"],
    resource_deps=nats_deps,
)

# thump-bootstrap: a Helm pre-install/pre-upgrade hook Job (job-bootstrap.yaml)
# that calls broker.ConnectAndEnsure against NATS. Under a real `helm install`
# the hook-weight annotations order it relative to other hooks, but Tilt's
# helm() only templates the chart — it doesn't run Helm's hook lifecycle, so
# nothing here stops this Job from racing NATS. Without this resource_deps,
# it dialed a NATS Service with no pod behind it yet and burned through
# backoffLimit before NATS ever came up (2026-07-29 incident).
k8s_resource("thump-bootstrap", labels=["broker"], resource_deps=["nats", "thump-s3-secret"])

for beat in ["rattle", "clank", "hiss", "thump"]:
    # thump-bootstrap creates the streams/consumers beat.AwaitConsumers binds to
    # on startup (clank, hiss, thump all call it) — that call is a single lookup
    # with no retry, so racing ahead of bootstrap means an immediate exit(1) and
    # a k8s restart, which is what "fixes itself" a restart or two later without
    # this dep (2026-07-29 incident, part 2: same race one edge further down).
    deps = [
        "thump-registry",
        "nats",
        "thump-bootstrap",
        "thump-s3-secret",
    ]  # ← every beat ships its WAL to S3
    if beat == "clank":
        deps.append("thump-anthropic-secret")  # ← or until its Secret does
    if beat in ("clank", "hiss"):
        deps.append("thump-seal-secret")  # ← only these two mount seal.secretName
    if beat == "hiss":
        deps.append("approvalrequests.thump.dev")

    k8s_resource(
        beat,
        labels=["machine"],
        resource_deps=deps,
        trigger_mode=TRIGGER_MODE_MANUAL,  # same "you decide when it wakes" posture (W0-4)
    )

# s3mock (cluster=dev only): adobe/s3mock replaces MinIO as this profile's
# WAL/transcript durability layer (2026-08-11 — MinIO was this PR's original
# choice, but Ian had already settled on S3Mock for local dev on 2026-07-07;
# lighter on laptop memory, and it needs no bucket-creation Job — the
# manifest's COM_ADOBE_TESTING_S3MOCK_STORE_INITIAL_BUCKETS env var does
# that on startup). Full manifest: deploy/dev/manifests/s3mock.yaml.
#
# No resource_deps: unlike MinIO (installed by dev-substrate's Helm chain),
# S3Mock needs nothing dev-substrate provides — it comes up in parallel with
# dev-substrate's ~10-15 minute run, same as thump-registry.
#
# Every beat dials S3Mock's plain HTTP port (9090), not its bundled
# self-signed-cert HTTPS port (9191, still exposed for a convenience curl
# from a debug pod). Declared plaintext rather than TLS with peer
# verification skipped: I-16 (docs/invariants.md) forbids InsecureSkipVerify
# categorically, and already accepts exactly this shape for Prometheus/Loki
# — a vendored dev backend that doesn't serve real TLS, riding node-to-node
# Cilium WireGuard instead. internal/config/config.go's
# RequireURL("S3_ENDPOINT", "http", "https") is where that's enforced.
if cluster_name == "dev":
    k8s_yaml("deploy/dev/manifests/s3mock.yaml")
    k8s_resource("s3mock", labels=["broker"])

# thump-notify-echo: a Slack-webhook stand-in for verifying the notify wire
# format before a live session (phase-af-cut-not-clobber.md Step 0c).
# slack.Webhook (internal/notify/slack/slack.go) posts a POST {"text": ...}
# to whatever URL it's given and only cares about the 2xx it gets back —
# nothing about the wire is Slack-specific, so an echo receiver proves the
# contract without a real webhook URL. mendhak/http-https-echo logs every
# request (headers + body) to stdout, so `kubectl logs
# deploy/thump-notify-echo -n thump` shows the digest slack.digest()
# rendered, no debugger needed.
#
# thump-test only: it's the one profile whose values file points
# notify.slackWebhookURL here (deploy/tilt-values-thump-test.yaml). Raw
# k8s_yaml(), not part of the chart — this is dev-session scaffolding, not
# something a real `helm install` should ever carry, and it never touches
# ~/projects/ceph/thump-test's GitOps tree (that repo is public; see Step
# 4b's disclosure argument for why nothing test-only belongs there). The
# "thump" namespace already exists by this point (ENSURE_NS's local() call
# above runs before any k8s_yaml), so no resource_deps is needed.
if cluster_name == "thump-test":
    k8s_yaml(blob("""
apiVersion: apps/v1
kind: Deployment
metadata:
  name: thump-notify-echo
  namespace: thump
spec:
  replicas: 1
  selector:
    matchLabels: {app: thump-notify-echo}
  template:
    metadata:
      labels: {app: thump-notify-echo}
    spec:
      containers:
        - name: echo
          image: mendhak/http-https-echo:31
          ports:
            - containerPort: 8080
          env:
            - name: HTTP_PORT
              value: "8080"
---
apiVersion: v1
kind: Service
metadata:
  name: thump-notify-echo
  namespace: thump
spec:
  selector: {app: thump-notify-echo}
  ports:
    - port: 8080
      targetPort: 8080
"""))
    k8s_resource("thump-notify-echo", labels=["infra"])

# dev-only convenience port-forwards. The Services below are installed by
# deploy/dev/bootstrap.sh's own helm releases, not by this Tiltfile's
# k8s_yaml(rendered) — Tilt has no object-graph knowledge of them to attach
# a k8s_resource() port_forward to, so each gets its own local_resource
# running `kubectl port-forward` as a supervised serve_cmd instead. No
# Gateway API/Ingress in this env (deploy/dev/values/cilium.yaml's
# gatewayAPI.enabled: false) — this is the whole "reach the UI" story.
if cluster_name == "dev":
    def dev_port_forward(name, service, namespace, local_port, remote_port):
        local_resource(
            name,
            serve_cmd="kubectl --context " + cluster["context"]
            + " -n " + namespace + " port-forward svc/" + service
            + " " + str(local_port) + ":" + str(remote_port),
            resource_deps=["dev-substrate"],
            labels=["ui"],
        )

    dev_port_forward("grafana-ui", "grafana", "monitoring", 3000, 80)
    dev_port_forward("prometheus-ui", "prometheus-kube-prometheus-prometheus", "monitoring", 9090, 9090)
    dev_port_forward("hubble-ui", "hubble-ui", "kube-system", 12000, 80)
    dev_port_forward("otel-demo-ui", "frontend-proxy", "otel-demo", 8080, 8080)
