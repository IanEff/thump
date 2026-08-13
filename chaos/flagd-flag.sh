#!/bin/bash
# thump chaos — flagd-flag.sh (targets otel-demo / flagd-config)
#
# Flips any flagd-config flag's defaultVariant. Generalises
# flagd-cart-failure.sh, which only ever touched cartFailure — the live
# ConfigMap carries 16 flags and every one of them is reachable through the
# same merge-patch mechanism.
#
# Usage: flagd-flag.sh <flag> [variant] [inject|restore]
#   flagd-flag.sh cartFailure on inject
#   flagd-flag.sh cartFailure off restore
# variant defaults to "on" for inject, "off" for restore, matching every
# boolean fault flag in the demo's own flags.json — a non-boolean flag
# (paymentFailure's graduated percentages, imageSlowLoad's durations) needs
# its variant named explicitly either way.
set -euo pipefail

NAMESPACE="otel-demo"
CONFIGMAP="flagd-config"
DATA_KEY="demo.flagd.json"

FLAG="${1:-}"
ACTION="${2:-inject}"
VARIANT="${3:-}"

if [ -z "$FLAG" ]; then
    echo "Usage: $0 <flag> [inject|restore] [variant]" >&2
    exit 1
fi

if [ -z "$VARIANT" ]; then
    case "$ACTION" in
        inject|on) VARIANT="on" ;;
        restore|off) VARIANT="off" ;;
    esac
fi

_get_blob() {
    # jsonpath treats an unescaped "." as a field-nesting separator, so
    # {.data.demo.flagd.json} parses as three levels of nesting rather than
    # the ConfigMap's one literal key "demo.flagd.json" — silently matches
    # nothing and prints an empty string, every single call. Found live,
    # 2026-08-06 (flagd-cart-failure.sh): this is what actually emptied the
    # ConfigMap on the very first live run, not a transient kubectl hiccup as
    # first suspected — _patch_variant's own guard below refuses to patch on
    # this rather than writing the empty result back.
    kubectl get configmap "$CONFIGMAP" -n "$NAMESPACE" -o jsonpath="{.data.demo\.flagd\.json}"
}

_patch_variant() {
    local flag="$1" variant="$2"
    local current_blob updated_json

    current_blob=$(_get_blob)

    # jq on empty/malformed stdin with this filter exits 0 and prints
    # nothing — set -e never trips, and the naive version of this went on to
    # overwrite the whole ConfigMap with an empty value on a transient
    # `kubectl get` hiccup. Found live, 2026-08-06 (flagd-cart-failure.sh):
    # flagd read the empty file, warned, and otel-demo ran on whatever it had
    # cached in memory until this was caught and the ConfigMap restored by
    # hand. Validate the blob is non-empty JSON and the flag actually exists
    # before writing anything back.
    if ! printf '%s' "$current_blob" | jq -e --arg f "$flag" '.flags[$f]' >/dev/null 2>&1; then
        echo "refusing to patch: ${NAMESPACE}/${CONFIGMAP}'s ${DATA_KEY} did not read back as valid flags JSON, or has no flag named ${flag}" >&2
        exit 1
    fi

    updated_json=$(printf '%s' "$current_blob" | jq --arg f "$flag" --arg v "$variant" '.flags[$f].defaultVariant = $v')

    kubectl create configmap "$CONFIGMAP" \
        -n "$NAMESPACE" \
        --from-literal="${DATA_KEY}=${updated_json}" \
        --dry-run=client -o yaml | kubectl apply -f -
}

case "$ACTION" in
    inject|on|restore|off)
        echo "Setting ${FLAG}.defaultVariant=${VARIANT} in ${NAMESPACE}/${CONFIGMAP}..."
        _patch_variant "$FLAG" "$VARIANT"
        ;;
    *)
        echo "Usage: $0 <flag> [inject|restore] [variant]" >&2
        exit 1
        ;;
esac
