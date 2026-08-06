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
    # jsonpath treats an unescaped "." as a field-nesting separator, so
    # {.data.demo.flagd.json} parses as three levels of nesting rather than
    # the ConfigMap's one literal key "demo.flagd.json" — silently matches
    # nothing and prints an empty string, every single call. Found live,
    # 2026-08-06: this is what actually emptied the ConfigMap on the very
    # first live run, not a transient kubectl hiccup as first suspected —
    # _patch_variant's own new guard (below) now refuses to patch on this
    # rather than writing the empty result back.
    kubectl get configmap "$CONFIGMAP" -n "$NAMESPACE" -o jsonpath="{.data.demo\.flagd\.json}"
}

_patch_variant() {
    local variant="$1"
    local current_blob updated_json
    current_blob=$(_get_blob)

    # jq on empty/malformed stdin with this filter exits 0 and prints
    # nothing — set -e never trips, and the naive version of this function
    # went on to overwrite the whole ConfigMap with an empty value on a
    # transient `kubectl get` hiccup. Found live, 2026-08-06: flagd read the
    # empty file, warned, and otel-demo ran on whatever it had cached in
    # memory until this was caught and the ConfigMap restored by hand from
    # the chart's own demo.flagd.json. Validate the blob is non-empty JSON
    # with the field we're about to touch before writing anything back.
    if ! printf '%s' "$current_blob" | jq -e '.flags.cartFailure' >/dev/null 2>&1; then
        echo "refusing to patch: ${NAMESPACE}/${CONFIGMAP}'s ${DATA_KEY} did not read back as valid flags JSON" >&2
        exit 1
    fi

    updated_json=$(printf '%s' "$current_blob" | jq --arg v "$variant" '.flags.cartFailure.defaultVariant = $v')

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
