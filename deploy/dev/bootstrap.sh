#!/usr/bin/env bash
# Idempotent substrate installer for the local thump dev environment.
# Invoked by the Tiltfile's dev-substrate local_resource (cluster=dev only),
# wrapped in guarded() the same as every other Tilt-driven kubectl/helm call
# in this repo. Every step below is `helm upgrade --install --wait` or
# `kubectl apply`, both idempotent, so rerunning this on an
# already-provisioned cluster converges rather than erroring.
#
# Order is NOT arbitrary — two hard dependencies:
#   1. prometheus-operator-crds MUST land before cilium: Cilium's chart
#      validate.yaml template refuses to render any ServiceMonitor without
#      that CRD present (same reasoning the rig's ArgoCD sync waves -11/-10
#      encode).
#   2. cert-manager MUST land (and its CRDs be Established) before
#      cluster-issuer.yaml is applied — that manifest is Issuer/
#      Certificate/ClusterIssuer, cert-manager's own CRDs.
set -euo pipefail

DEV_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Every kubectl/helm call below must be pinned to this context explicitly —
# unlike the rest of the Tiltfile, which always passes --context, relying on
# the kubeconfig's ambient current-context is a live footgun: the Mac runs
# other clusters/sessions that can leave current-context pointed anywhere
# (localhost:8080's stub "default", a GKE rig, kind-dev, ...). Found live:
# every call here silently ran against current-context "default" and failed
# with "connection refused" on ensure_ns's first apply.
CTX="k3d-thump-dev"
kubectl() { command kubectl --context "$CTX" "$@"; }
helm() { command helm --kube-context "$CTX" "$@"; }

echo "== thump dev substrate: bootstrap starting ==" >&2

# --force-update: plain `helm repo add` hard-fails (exit 1, not a skip) if
# the name already exists with a URL differing only by e.g. a trailing
# slash — hit live adding cilium/ on a host that already had it added
# without one. --force-update makes every line here idempotent regardless
# of how the repo got added previously.
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update >/dev/null
helm repo add cilium https://helm.cilium.io --force-update >/dev/null
helm repo add jetstack https://charts.jetstack.io --force-update >/dev/null
helm repo add grafana https://grafana.github.io/helm-charts --force-update >/dev/null
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts --force-update >/dev/null
# Scoped to the five repos this script owns, not a bare `helm repo update` —
# a colleague's laptop may have unrelated broken repos configured from other
# projects (hit live: a stale bitnami-labs entry made a bare update exit 1
# even though every repo this script needs updated fine), and that shouldn't
# block bootstrap here.
helm repo update prometheus-community cilium jetstack grafana open-telemetry >/dev/null

ensure_ns() {
  kubectl create namespace "$1" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

# 1. prometheus-operator-crds — before cilium (see header).
ensure_ns monitoring
echo "-- prometheus-operator-crds --" >&2
helm upgrade --install prometheus-operator-crds prometheus-community/prometheus-operator-crds \
  --version 30.0.1 \
  --namespace monitoring \
  --wait

# 2. cilium
#
# k3d.yaml's --cluster-cidr and cilium.yaml's clusterPoolIPv4PodCIDRList
# describe the same pod CIDR from two config files with no shared source —
# cilium ships pods no route can reach if they drift, and neither file's own
# comment cross-reference is enforced anywhere. Checked here since this is
# the one place both are read before cilium starts allocating from either.
k3d_cidr=$(grep -o 'cluster-cidr=[0-9./]*' "$DEV_DIR/k3d.yaml" | cut -d= -f2)
cilium_cidr=$(grep -o 'clusterPoolIPv4PodCIDRList: \["[0-9./]*"\]' "$DEV_DIR/values/cilium.yaml" | grep -o '[0-9]*\.[0-9]*\.[0-9]*\.[0-9]*/[0-9]*')
if [ "$k3d_cidr" != "$cilium_cidr" ]; then
  echo "thump: k3d.yaml's --cluster-cidr ($k3d_cidr) != cilium.yaml's clusterPoolIPv4PodCIDRList ($cilium_cidr) — cilium would allocate pod IPs the CNI can't route" >&2
  exit 1
fi

echo "-- cilium --" >&2
helm upgrade --install cilium cilium/cilium \
  --version 1.19.3 \
  --namespace kube-system \
  --values "$DEV_DIR/values/cilium.yaml" \
  --wait

# 3. cert-manager + the private-PKI ClusterIssuer chain
echo "-- cert-manager --" >&2
helm upgrade --install cert-manager jetstack/cert-manager \
  --version v1.21.0 \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true \
  --wait
kubectl apply -f "$DEV_DIR/manifests/cluster-issuer.yaml"
echo "waiting for thump-ca Certificate to be Ready..." >&2
kubectl wait --for=condition=Ready certificate/thump-ca -n cert-manager --timeout=120s

# 4. kube-prometheus-stack (Prometheus + Alertmanager + kube-state-metrics + node-exporter)
echo "-- kube-prometheus-stack --" >&2
helm upgrade --install prometheus prometheus-community/kube-prometheus-stack \
  --version 87.15.2 \
  --namespace monitoring \
  --values "$DEV_DIR/values/kube-prometheus-stack.yaml" \
  --wait

# 5. Grafana, Tempo, OTel Collector, Loki, Promtail
echo "-- grafana --" >&2
helm upgrade --install grafana grafana/grafana \
  --version 10.5.15 \
  --namespace monitoring \
  --values "$DEV_DIR/values/grafana.yaml" \
  --wait

ensure_ns tracing
echo "-- tempo --" >&2
helm upgrade --install tempo grafana/tempo \
  --version 1.10.1 \
  --namespace tracing \
  --values "$DEV_DIR/values/tempo.yaml" \
  --wait

echo "-- otel-collector --" >&2
helm upgrade --install otel-collector open-telemetry/opentelemetry-collector \
  --version 0.100.0 \
  --namespace tracing \
  --values "$DEV_DIR/values/otel-collector.yaml" \
  --wait

ensure_ns logging
echo "-- loki --" >&2
helm upgrade --install loki grafana/loki \
  --version 6.6.2 \
  --namespace logging \
  --values "$DEV_DIR/values/loki.yaml" \
  --wait

echo "-- promtail --" >&2
helm upgrade --install promtail grafana/promtail \
  --version 6.16.2 \
  --namespace logging \
  --values "$DEV_DIR/values/promtail.yaml" \
  --wait

# 6. S3Mock — the WAL/transcript durability layer (rigs use a real GCS
# bucket instead) — is deployed by the Tiltfile itself
# (deploy/dev/manifests/s3mock.yaml), not by this script: it's a single
# stateless Deployment+Service with no Helm chart, and every other
# Tilt-managed (not out-of-band-provisioned) object in this profile already
# lives in the Tiltfile rather than here.

# 7. SLO burn-rate rules — no Sloth operator, see slo-rules.yaml's header.
echo "-- slo-rules --" >&2
kubectl apply -f "$DEV_DIR/manifests/slo-rules.yaml"

# 7b. thump's own pipeline dashboard — picked up by the Grafana sidecar
# (values/grafana.yaml's sidecar.dashboards), not this script; applying it
# here only needs to happen once, same as slo-rules.
echo "-- dashboard-thump --" >&2
kubectl apply -f "$DEV_DIR/manifests/dashboard-thump.yaml"

# 8. OTel demo (Astronomy Shop) — the app-under-test.
ensure_ns otel-demo
echo "-- otel-demo --" >&2
helm upgrade --install otel-demo open-telemetry/opentelemetry-demo \
  --version 0.40.10 \
  --namespace otel-demo \
  --values "$DEV_DIR/values/otel-demo.yaml" \
  --wait \
  --timeout 10m

echo "== thump dev substrate: bootstrap complete ==" >&2
