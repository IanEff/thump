#!/bin/bash
# thump chaos — flagd-cart-failure.sh (targets otel-demo / flagd-config)
#
# Thin wrapper over flagd-flag.sh, kept so task chaos:cart-failure and
# chaos/scenarios.yaml's cart-failure row don't need editing. New flags
# should call flagd-flag.sh directly rather than growing a new one-flag
# wrapper each time.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ACTION="${1:-inject}"

case "$ACTION" in
    inject|on)
        "$DIR/flagd-flag.sh" cartFailure inject on
        ;;
    restore|off)
        "$DIR/flagd-flag.sh" cartFailure restore off
        ;;
    *)
        echo "Usage: $0 [inject|restore]" >&2
        exit 1
        ;;
esac
