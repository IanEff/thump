# Tiltfile — root orchestrator for the thump dev loop.
#
# Two cluster profiles:
#   dev        — fully-local k3d cluster (docs/dev-environment.md). `task
#                dev:up` creates it; deploy/dev/bootstrap.sh installs the
#                substrate. platform: None (k3d shares the host kernel,
#                no cross-compile). registry: k3d's built-in
#                thump-dev-registry.
#   thump-test — GCE/k3s/Cilium rig (~/projects/ceph/thump-test), IAP-only.
#                `gcloud compute start-iap-tunnel thump-test-control-plane
#                6443 ...` must already be running. platform: linux/amd64
#                (real GCE VMs). registry: ghcr.io/ianeff.
#
# ceph-lab, rook-gke, rook-gce-k3s pruned — see git history.
#
# Each layer is a separate Tiltfile under tilt/, load()'d here and called in
# dependency order:
#   lib    → pure helper functions (guarded, kubectl_local, ensure_ns_cmd)
#   infra  → namespace, secrets, domain/CRD gates
#   build  → otelc-native binary, custom_build for all 5 beats
#   deploy → helm chart rendering, filter_yaml, k8s_resource wiring
#   dev    → s3mock, port-forwards (dev only)
#   thump_test → notify-echo fixture (thump-test only)

load("./tilt/lib.Tiltfile", "make_helpers")
load("./tilt/infra.Tiltfile", infra_setup = "setup")
load("./tilt/build.Tiltfile", build_setup = "setup")
load("./tilt/deploy.Tiltfile", deploy_setup = "setup")
load("./tilt/dev.Tiltfile", dev_setup = "setup")
load("./tilt/thump_test.Tiltfile", thump_test_setup = "setup")

CLUSTERS = {
    "dev": {
        "context": "k3d-thump-dev",
        "platform": None,
        "registry": "thump-dev-registry:5050",
        "values": "deploy/tilt-values-dev.yaml",
    },
    "thump-test": {
        "context": "thump-test",
        "platform": "linux/amd64",
        "registry": "ghcr.io/ianeff",
        "values": "deploy/tilt-values-thump-test.yaml",
    },
}

config.define_string(
    "cluster",
    usage = "which cluster to target: dev (default) or thump-test",
)
cfg = config.parse()
cluster_name = cfg.get("cluster", "dev")
if cluster_name not in CLUSTERS:
    fail("unknown --cluster %s — must be one of: %s" % (cluster_name, ", ".join(CLUSTERS.keys())))
cluster = CLUSTERS[cluster_name]

# Named explicitly (not k8s_context()) so a context typo fails loud instead
# of silently allow-listing whatever's currently active.
allow_k8s_contexts(cluster["context"])

# --- Shared helpers ---
h = make_helpers(cluster, cluster_name)

# --- Infrastructure: namespace, secrets, domain/CRD gates ---
infra = infra_setup(cluster, cluster_name, h)

# --- Build: otelc-native binary, custom_build for all 5 beats ---
build_setup(cluster)

# --- Deploy: helm chart, filter_yaml, k8s_resource wiring ---
deploy_setup(cluster, cluster_name, infra.domain_values, infra.domains)

# --- Profile-specific extras ---
if cluster_name == "dev":
    dev_setup(cluster)
if cluster_name == "thump-test":
    thump_test_setup()
