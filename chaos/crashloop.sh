#!/bin/bash
# thump chaos — crashloop.sh (generalist: namespace/deployment/container by arg)
#
# Patches a Deployment's container memory limit below what the process needs,
# producing a kernel-enforced OOMKilled / CrashLoopBackOff pair.
# - inject:  set memory limit to LOW_MEM (default 8Mi)
# - restore: set memory limit back to RESTORE_MEM (default 64Mi)
set -euo pipefail

NAMESPACE="${NAMESPACE:-otel-demo}"
DEPLOYMENT="${DEPLOYMENT:-product-catalog}"
CONTAINER="${CONTAINER:-$DEPLOYMENT}"
LOW_MEM="${LOW_MEM:-8Mi}"
RESTORE_MEM="${RESTORE_MEM:-64Mi}"
ACTION="${1:-inject}"

case "$ACTION" in
    inject|on)
        echo "Patching ${NAMESPACE}/${DEPLOYMENT} container ${CONTAINER} memory limit -> ${LOW_MEM}..."
        kubectl -n "$NAMESPACE" patch deployment "$DEPLOYMENT" --type=json -p="[
          {\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/resources/limits/memory\", \"value\": \"${LOW_MEM}\"}
        ]"
        ;;
    restore|off)
        echo "Restoring ${NAMESPACE}/${DEPLOYMENT} container ${CONTAINER} memory limit -> ${RESTORE_MEM}..."
        kubectl -n "$NAMESPACE" patch deployment "$DEPLOYMENT" --type=json -p="[
          {\"op\": \"replace\", \"path\": \"/spec/template/spec/containers/0/resources/limits/memory\", \"value\": \"${RESTORE_MEM}\"}
        ]"
        ;;
    *)
        echo "Usage: $0 [inject|restore]" >&2
        exit 1
        ;;
esac
