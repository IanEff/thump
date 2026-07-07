# Outside knowledge — verified against docs.tilt.dev during authoring
# (2026-07-05), not carried over from prior training. See
# phase2-ws2-deploy-guide.md Stage 3 for the W0-1..W0-4 decisions this rests
# on, and thump-running-notes for the k3s-on-Lima registry plumbing this
# depends on.

# `ceph-lab` doesn't match any of Tilt's known-local context names (kind-*,
# k3d-*, minikube, docker-desktop, ...), so Tilt refuses to deploy to it by
# default as a safety rail against accidentally hitting a real cluster.
# Named explicitly (not k8s_context()) so a context typo fails loud instead
# of silently allow-listing whatever's currently active.
allow_k8s_contexts('ceph-lab')

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

DEV_REGISTRY = '192.168.56.1:5005'

# COMMIT is resolved once at Tiltfile load (not per-build), so it reflects
# the last commit, not uncommitted dirty-tree edits, and only refreshes on
# Tiltfile reload / the next `tilt up` — an accepted tradeoff for a fast
# edit loop here. DATE is deliberately left as the Dockerfile's "unknown"
# default under Tilt — a per-build wall-clock stamp isn't worth the noise.
COMMIT = str(local('git rev-parse --short --verify HEAD || echo none')).strip()

for beat in ['rattle', 'clank', 'hiss', 'thump']:
    docker_build(DEV_REGISTRY + '/thump-' + beat, '.', build_args={'BEAT': beat, 'VERSION': 'dev', 'COMMIT': COMMIT})

k8s_yaml(helm('deploy/chart/thump', values=['deploy/tilt-values.yaml']))

# Bring up NATS first — the beats dial it on boot; bring it up (and Ready) before them.
k8s_resource('nats', labels=['broker'], resource_deps=['thump-registry'])

for beat in ['rattle', 'clank', 'hiss', 'thump']:
    k8s_resource(
        beat,
        labels=['machine'],
        resource_deps=['thump-registry', 'nats'],   # ← don't start a beat until NATS exists
        trigger_mode=TRIGGER_MODE_MANUAL,            # same "you decide when it wakes" posture (W0-4)
    )
