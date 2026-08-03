#!/bin/bash
# thump chaos — flagd-cart-failure.sh (targets otel-demo / flagd-config)
#
# Flips the cartFailure defaultVariant flag in otel-demo's flagd-config ConfigMap.
# - inject: sets cartFailure.defaultVariant -> "on" (triggers service_failure -> disable-cart- failure)
# - restore: sets cartFailure.defaultVariant -> "off"
set -euo pipefail

NAMESPACE="otel-demo"
CONFIGMAP="flagd-config"
DATA_KEY="demo.flagd.json"
ACTION="${1:-inject}"

_get_blob() {
    kubectl get configmap "$CONFIGMAP" -n "$NAMESPACE" -o jsonpath="{.data.${DATA_KEY}}"
}

_patch_variant() {
    local variant="$1"
    local updated_json
    updated_json=$(_get_blob | jq --arg v "$variant" '.flags.cartFailure.defaultVariant = $v')

    # Escape JSON for Strategic Merge Patch / ConfigMap data key overwrite
    kubectl create configmap "$CONFIGMAP" \
        -n "$NAMESPACE" \
        --from-literal="${DATA_KEY}=${updated_json}" \
        --dry-run=client -o yaml | kubectl apply -f -
}

case "$ACTION" in
    inject|on)
        echo "Injecting cartFailure=on into ${NAMESPACE}/${CONFIGMAP}..."
        _patch_variant "on"
        ;;
    restore|off)
        echo "Restoring cartFailure=off in ${NAMESPACE}/${CONFIGMAP}..."
        _patch_variant "off"
        ;;
    *)
        echo "Usage: $0 [inject|restore]" >&2
        exit 1
        ;;
esac
