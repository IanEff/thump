#!/bin/bash
# thump chaos — preflight.sh
# Verifies engine and cluster preconditions before executing a chaos scenario.
set -euo pipefail

echo "=== Thump Scenario Preflight Check ==="

# 1. Check Namespace Existence (Tilt GC guard)
for ns in thump otel-demo rook-ceph; do
    if kubectl get ns "$ns" >/dev/null 2>&1; then
        echo "[OK] Namespace '$ns' exists"
    else
        echo "[FAIL] Namespace '$ns' missing! Ensure cluster and Tilt are active." >&2
        exit 1
    fi
done

# 2. Check Execution Mode & Killswitch state in running beat
echo "Checking thump deployment environment..."
EXECUTOR=$(kubectl get deploy -n thump -o jsonpath='{.items[*].spec.template.spec.containers[*].env[?(@.name=="THUMP_EXECUTOR")].value}' 2>/dev/null || echo "unknown")
echo "[INFO] THUMP_EXECUTOR=$EXECUTOR"

# 3. Check flagd ConfigMap availability
if kubectl get configmap flagd-config -n otel-demo >/dev/null 2>&1; then
    echo "[OK] otel-demo/flagd-config is accessible"
else
    echo "[WARN] otel-demo/flagd-config missing or inaccessible"
fi

# 4. Disable ArgoCD self-healing on Rook and workload applications
echo "Disabling ArgoCD self-healing on Rook and demo applications..."
ROOK_APPS=(
  "rook-storage"
  "rook-dashboards"
  "rook-gateway"
  "rook-operator"
  "rook-cluster"
  "opentelemetry-demo"
)

for app in "${ROOK_APPS[@]}"; do
  if kubectl get app "$app" -n argocd >/dev/null 2>&1; then
    echo "  -> Disabling selfHeal on argocd/app/$app"
    kubectl patch app "$app" -n argocd \
      --type merge \
      -p '{"spec":{"syncPolicy":{"automated":{"selfHeal":false}}}}'
  fi
done

echo "=== Preflight Passed ==="
