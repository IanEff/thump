#!/bin/bash
# thump chaos — acme-fault.sh (targets acme / acme-fault-flag)
#
# Flips the target key in acme's acme-fault-flag ConfigMap.
# - inject: sets target -> "/fail"
# - restore: sets target -> "/"
set -euo pipefail

NAMESPACE="acme"
CONFIGMAP="acme-fault-flag"
ACTION="${1:-inject}"

case "$ACTION" in
    inject|on)
        echo "Injecting target=/fail into ${NAMESPACE}/${CONFIGMAP}..."
        kubectl -n "$NAMESPACE" create configmap "$CONFIGMAP" --from-literal=target=/fail --dry-run=client -o yaml | kubectl apply -f -
        ;;
    restore|off)
        echo "Restoring target=/ in ${NAMESPACE}/${CONFIGMAP}..."
        kubectl -n "$NAMESPACE" create configmap "$CONFIGMAP" --from-literal=target=/ --dry-run=client -o yaml | kubectl apply -f -
        ;;
    *)
        echo "Usage: $0 [inject|restore]" >&2
        exit 1
        ;;
esac
