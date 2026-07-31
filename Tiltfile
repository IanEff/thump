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

# thump-anthropic-secret: clank's Secret is meant to pre-exist out-of-band
# (the lab's SOPS flow owns it in prod — see deploy/chart/thump/templates/
# secret.yaml's comment). Under Tilt there's no SOPS flow, so this is the dev
# stand-in: read the key from a gitignored .env (ANTHROPIC_API_KEY="...")
# and apply the Secret directly via kubectl, never through
# anthropic.create/apiKey in a values file (that stays a `--set`-only
# escape hatch, per the chart's own warning against committing a plaintext
# key). --dry-run=client -o yaml | apply, not `create`, so re-running this
# after rotating the key in .env updates the Secret instead of no-op'ing.
local_resource(
    "thump-anthropic-secret",
    cmd="bash -c '"
    + 'set -a; source .env 2>/dev/null || { echo ".env not found at repo root — expected ANTHROPIC_API_KEY=\\"...\\""  >&2; exit 1; }; set +a; '
    + '[ -n "$ANTHROPIC_API_KEY" ] || { echo "ANTHROPIC_API_KEY not set in .env" >&2; exit 1; }; '
    + "kubectl --context "
    + cluster["context"]
    + " create namespace thump --dry-run=client -o yaml | kubectl --context "
    + cluster["context"]
    + " apply -f - >/dev/null && "
    + "kubectl --context "
    + cluster["context"]
    + " -n thump create secret generic thump-anthropic "
    + '--from-literal=api-key="$ANTHROPIC_API_KEY" --dry-run=client -o yaml | kubectl --context '
    + cluster["context"]
    + " apply -f -"
    + "'",
    labels=["infra"],
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
local_resource(
    "thump-s3-secret",
    cmd="bash -c '"
    + 'set -a; source .env 2>/dev/null || { echo ".env not found at repo root — expected S3_ENDPOINT/S3_BUCKET/S3_ACCESS_KEY/S3_SECRET_KEY" >&2; exit 1; }; set +a; '
    + "for v in S3_ENDPOINT S3_BUCKET S3_ACCESS_KEY S3_SECRET_KEY; do "
    + '  [ -n "${!v}" ] || { echo "$v not set in .env" >&2; exit 1; }; '
    + "done; "
    + "kubectl --context "
    + cluster["context"]
    + " create namespace thump --dry-run=client -o yaml | kubectl --context "
    + cluster["context"]
    + " apply -f - >/dev/null && "
    + "kubectl --context "
    + cluster["context"]
    + " -n thump create secret generic thump-s3 "
    + '--from-literal=endpoint="$S3_ENDPOINT" --from-literal=bucket="$S3_BUCKET" '
    + '--from-literal=access-key="$S3_ACCESS_KEY" --from-literal=secret-key="$S3_SECRET_KEY" '
    + "--dry-run=client -o yaml | kubectl --context "
    + cluster["context"]
    + " apply -f -"
    + "'",
    labels=["infra"],
)

# thump-seal-secret / thump-nats-js-key-secret: unlike thump-anthropic-secret
# and thump-s3-secret above, these two keys are pure internal material — no
# external system issues them, so there's nothing to source from .env. The
# chart's own secret.yaml self-provisions them on a real `helm install` via
# a `lookup`-guarded block, but `lookup` always reads empty under `helm
# template` (what Tilt's helm() runs), so under Tilt that block would mint a
# fresh random key on every Tiltfile reload and silently break whatever it
# already sealed on disk. These local_resources are the Tilt-loop equivalent
# of the chart's lookup guard: create-if-absent, never touch an existing one
# (unlike thump-s3-secret's dry-run-apply, which is meant to re-sync every
# run) — `kubectl get ... || kubectl create ...`, not `--dry-run=client -o
# yaml | apply`.
local_resource(
    "thump-seal-secret",
    cmd="bash -c '"
    + "kubectl --context "
    + cluster["context"]
    + " create namespace thump --dry-run=client -o yaml | kubectl --context "
    + cluster["context"]
    + " apply -f - >/dev/null && "
    + "kubectl --context "
    + cluster["context"]
    + " -n thump get secret thump-seal >/dev/null 2>&1 || "
    + "kubectl --context "
    + cluster["context"]
    + ' -n thump create secret generic thump-seal --from-literal=key="$(openssl rand -base64 32)"'
    + "'",
    labels=["infra"],
)

local_resource(
    "thump-nats-js-key-secret",
    cmd="bash -c '"
    + "kubectl --context "
    + cluster["context"]
    + " create namespace thump --dry-run=client -o yaml | kubectl --context "
    + cluster["context"]
    + " apply -f - >/dev/null && "
    + "kubectl --context "
    + cluster["context"]
    + " -n thump get secret nats-js-key >/dev/null 2>&1 || "
    + "kubectl --context "
    + cluster["context"]
    + ' -n thump create secret generic nats-js-key --from-literal=key="$(openssl rand -base64 32)"'
    + "'",
    labels=["infra"],
)

DEV_REGISTRY = cluster["registry"]

# COMMIT is resolved once at Tiltfile load (not per-build), so it reflects
# the last commit, not uncommitted dirty-tree edits, and only refreshes on
# Tiltfile reload / the next `tilt up` — an accepted tradeoff for a fast
# edit loop here. DATE is deliberately left as the Dockerfile's "unknown"
# default under Tilt — a per-build wall-clock stamp isn't worth the noise.
COMMIT = str(local("git rev-parse --verify HEAD || echo none")).strip()

for beat in ["rattle", "clank", "hiss", "thump", "bootstrap"]:
    # docker_build's platform= must be a string or omitted entirely — unlike
    # a plain Starlark/Python kwarg, it does NOT treat None as "unset".
    if cluster["platform"]:
        docker_build(
            DEV_REGISTRY + "/thump-" + beat,
            ".",
            build_args={"BEAT": beat, "VERSION": "dev", "COMMIT": COMMIT},
            platform=cluster["platform"],
        )
    else:
        docker_build(
            DEV_REGISTRY + "/thump-" + beat,
            ".",
            build_args={"BEAT": beat, "VERSION": "dev", "COMMIT": COMMIT},
        )


# helm()'s k8s_yaml() below runs `helm template`, which — like `helm
# upgrade` — never renders crds/. Applied directly instead, which also
# means Tilt live-reloads it on edit, nicer than crds/'s install-once
# behavior while this schema is still moving.
k8s_yaml("deploy/chart/thump/crds/approvalrequest.yaml")

k8s_yaml(helm("deploy/chart/thump", values=[cluster["values"]]))

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
