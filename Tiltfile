# Outside knowledge — verified against docs.tilt.dev during authoring
# (2026-07-05), not carried over from prior training. See
# phase2-ws2-deploy-guide.md Stage 3 for the W0-1..W0-4 decisions this rests
# on, and thump-running-notes for the k3s-on-Lima registry plumbing this
# depends on.

# Multi-cluster dev loop (phase2-converge-rook-gke-guide.md Stage 0.6
# addendum): everything that differs between ceph-lab and rook-gke —
# kubectl context, build platform, image registry, chart values overlay —
# lives in this one table, picked by `tilt up -- --cluster=<name>`.
#
# platform: None for ceph-lab (Lima's arm64 VMs match the Mac natively, no
# cross-compile needed); 'linux/amd64' for rook-gke (e2-standard-4 nodes are
# amd64, and CGO_ENABLED=0 means buildx cross-compiles for free, no QEMU —
# same trick as the release Dockerfile's $BUILDPLATFORM/$TARGETARCH staging).
# Deliberately single-platform per profile, not multi-arch: only one context
# is ever live at a time, so building the arch you're not deploying to is
# pure waste (the guide's own ruling).
#
# registry: ceph-lab's insecure local registry lives on the Mac's
# socket_vmnet subnet (192.168.56.0/24) and is reachable from Lima nodes only
# — GKE's nodes are on GCP's network and have no route to it at all. rook-gke
# has to push somewhere GKE can actually pull from, so it reuses ghcr.io/ianeff,
# the same registry the release path (`make images`) already pushes to.
CLUSTERS = {
    'ceph-lab': {
        'context': 'ceph-lab',
        'platform': None,
        'registry': '192.168.56.1:5005',
        'values': 'deploy/tilt-values.yaml',
    },
    'rook-gke': {
        'context': 'gke_terraform-sandbox-430820_us-central1_rook-ceph-gke',
        'platform': 'linux/amd64',
        'registry': 'ghcr.io/ianeff',
        'values': 'deploy/tilt-values-rook-gke.yaml',
    },
}

config.define_string('cluster', usage='which CLUSTERS profile to target: ceph-lab (default) or rook-gke')
cfg = config.parse()
cluster_name = cfg.get('cluster', 'ceph-lab')
if cluster_name not in CLUSTERS:
    fail('unknown --cluster %s — must be one of: %s' % (cluster_name, ', '.join(CLUSTERS.keys())))
cluster = CLUSTERS[cluster_name]

# `ceph-lab` doesn't match any of Tilt's known-local context names (kind-*,
# k3d-*, minikube, docker-desktop, ...), so Tilt refuses to deploy to it by
# default as a safety rail against accidentally hitting a real cluster.
# Named explicitly (not k8s_context()) so a context typo fails loud instead
# of silently allow-listing whatever's currently active. Same rail now
# guards rook-gke too: allow-listing only the ONE profile you asked for on
# the CLI means picking the wrong live kubectx still fails loud, not silent.
allow_k8s_contexts(cluster['context'])

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
    'thump-registry',
    cmd='docker inspect thump-registry >/dev/null 2>&1 || ' +
        'docker run -d --name thump-registry --restart unless-stopped ' +
        '-p 5005:5000 registry:2',
    labels=['infra'],
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
    'thump-anthropic-secret',
    cmd='bash -c \'' +
        'set -a; source .env 2>/dev/null || { echo ".env not found at repo root — expected ANTHROPIC_API_KEY=\\"...\\""  >&2; exit 1; }; set +a; ' +
        '[ -n "$ANTHROPIC_API_KEY" ] || { echo "ANTHROPIC_API_KEY not set in .env" >&2; exit 1; }; ' +
        'kubectl --context ' + cluster['context'] + ' create namespace thump --dry-run=client -o yaml | kubectl --context ' + cluster['context'] + ' apply -f - >/dev/null && ' +
        'kubectl --context ' + cluster['context'] + ' -n thump create secret generic thump-anthropic ' +
        '--from-literal=api-key="$ANTHROPIC_API_KEY" --dry-run=client -o yaml | kubectl --context ' + cluster['context'] + ' apply -f -' +
        '\'',
    labels=['infra'],
)

DEV_REGISTRY = cluster['registry']

# COMMIT is resolved once at Tiltfile load (not per-build), so it reflects
# the last commit, not uncommitted dirty-tree edits, and only refreshes on
# Tiltfile reload / the next `tilt up` — an accepted tradeoff for a fast
# edit loop here. DATE is deliberately left as the Dockerfile's "unknown"
# default under Tilt — a per-build wall-clock stamp isn't worth the noise.
COMMIT = str(local('git rev-parse --verify HEAD || echo none')).strip()

for beat in ['rattle', 'clank', 'hiss', 'thump']:
    # docker_build's platform= must be a string or omitted entirely — unlike
    # a plain Starlark/Python kwarg, it does NOT treat None as "unset".
    if cluster['platform']:
        docker_build(DEV_REGISTRY + '/thump-' + beat, '.', build_args={'BEAT': beat, 'VERSION': 'dev', 'COMMIT': COMMIT}, platform=cluster['platform'])
    else:
        docker_build(DEV_REGISTRY + '/thump-' + beat, '.', build_args={'BEAT': beat, 'VERSION': 'dev', 'COMMIT': COMMIT})

k8s_yaml(helm('deploy/chart/thump', values=[cluster['values']]))

# Bring up NATS first — the beats dial it on boot; bring it up (and Ready) before them.
k8s_resource('nats', labels=['broker'], resource_deps=['thump-registry'])

for beat in ['rattle', 'clank', 'hiss', 'thump']:
    deps = ['thump-registry', 'nats']   # ← don't start a beat until NATS exists
    if beat == 'clank':
        deps.append('thump-anthropic-secret')   # ← or until its Secret does
    k8s_resource(
        beat,
        labels=['machine'],
        resource_deps=deps,
        trigger_mode=TRIGGER_MODE_MANUAL,            # same "you decide when it wakes" posture (W0-4)
    )
