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
    preflight = (
        "waited=0; "
        + "until "
        + kubectl
        + " get --raw /readyz >/dev/null 2>&1; do "
        + "waited=$((waited+2)); "
        + "if [ $waited -ge 60 ]; then "
        + 'echo "thump: API server for context '
        + cluster["context"]
        + ' unreachable after ${waited}s — is the IAP tunnel up? (just tunnel, in the rig repo)" >&2; exit 1; '
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


def kubectl_local(name, body, labels=["infra"]):
    local_resource(name, cmd=guarded(name, body), labels=labels)


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
kubectl_local(
    "thump-s3-secret",
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
    + " apply -f -",
)

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
# unordered process: the namespace can still be missing when `tilt up` first
# renders. Since the whole non-workload manifest set (RBAC, ConfigMaps,
# Certificates, ...) applies as one sequential batch in Tilt's uncategorized
# resource, an early object failing on a missing namespace aborts every
# object queued after it in that same apply — observed live 2026-08-03:
# thump-acme-actuate:role failed on "namespaces \"acme\" not found" and took
# thump-nats:configmap and nats-tls:certificate down with it, despite neither
# having anything to do with acme. Pre-creating the namespace here (same
# idempotent, eval-time pattern as ENSURE_NS above, and just as harmless a
# no-op on profiles where the domain is disabled and the namespace already
# absent-and-unreferenced) closes the race instead of requiring a manual
# `tilt trigger uncategorized` retry once the rig repo's own provisioning
# catches up.
domain_values = read_yaml(cluster["values"], default={})
domains = domain_values.get("domains", {})
if domains.get("acme", {}).get("enabled", False):
    local(guarded("acme-namespace", ensure_ns_cmd("acme")), quiet=True, echo_off=True)
if domains.get("otelDemo", {}).get("enabled", False):
    local(guarded("otel-demo-namespace", ensure_ns_cmd("otel-demo")), quiet=True, echo_off=True)

DEV_REGISTRY = cluster["registry"]

# COMMIT is resolved once at Tiltfile load (not per-build), so it reflects
# the last commit, not uncommitted dirty-tree edits, and only refreshes on
# Tiltfile reload / the next `tilt up` — an accepted tradeoff for a fast
# edit loop here. DATE is deliberately left as the Dockerfile's "unknown"
# default under Tilt — a per-build wall-clock stamp isn't worth the noise.
COMMIT = str(local("git rev-parse --verify HEAD || echo none")).strip()

# Fast host-native compilation for local dev loop (Option 2).
# `go tool otelc setup` instruments the source tree on load (idempotent, ~1s).
local("go tool otelc setup ./cmd/bootstrap ./cmd/clank ./cmd/hiss ./cmd/rattle ./cmd/thump", quiet=True, echo_off=True)

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
        + "go build -ldflags '-s -w -X main.version=dev -X main.commit=" + COMMIT + "' "
        + "-o bin/dev/" + beat + " ./cmd/" + beat + " && "
        + "docker build " + platform_flag + "-f Dockerfile.dev --build-arg BEAT=" + beat + " -t $EXPECTED_REF bin/dev"
    )
    custom_build(
        DEV_REGISTRY + "/thump-" + beat,
        command=cmd,
        deps=["cmd/" + beat, "internal", "go.mod", "go.sum", ".otelc-build"],
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

# Bring up NATS first — the beats dial it on boot; bring it up (and Ready) before them.
k8s_resource(
    "nats", labels=["broker"], resource_deps=["thump-registry", "thump-nats-js-key-secret"]
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
