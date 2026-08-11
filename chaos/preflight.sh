#!/bin/bash
# thump chaos — preflight.sh
# Verifies engine and cluster preconditions before executing a chaos scenario.
set -euo pipefail

echo "=== Thump Scenario Preflight Check ==="

# 1. Check Namespace Existence (Tilt GC guard). "thump" is the one
# namespace every cluster profile carries — a hard failure there means the
# cluster or Tilt session itself is missing. otel-demo/rook-ceph are
# per-profile domains (the dev profile — docs/dev-environment.md — runs
# otel-demo and no Ceph at all), so their absence is a warning, not a
# reason to abort a scenario that doesn't touch them.
if kubectl get ns thump >/dev/null 2>&1; then
    echo "[OK] Namespace 'thump' exists"
else
    echo "[FAIL] Namespace 'thump' missing! Ensure cluster and Tilt are active." >&2
    exit 1
fi
for ns in otel-demo rook-ceph; do
    if kubectl get ns "$ns" >/dev/null 2>&1; then
        echo "[OK] Namespace '$ns' exists"
    else
        echo "[WARN] Namespace '$ns' missing — fine if this profile doesn't run that domain"
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

# 4. Disable ArgoCD self-healing on Rook and workload applications. The dev
# profile (docs/dev-environment.md) runs no ArgoCD at all (argocd.enabled:
# false in deploy/tilt-values-dev.yaml — the OTel demo's flagd-config there
# is patched directly, with no selfHeal to fight), so this whole step is
# skipped rather than failing on a missing "argocd" namespace.
if kubectl get ns argocd >/dev/null 2>&1; then
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
else
    echo "[INFO] No 'argocd' namespace — skipping selfHeal step (this profile runs no ArgoCD)"
fi

echo "=== Preflight Passed ==="
