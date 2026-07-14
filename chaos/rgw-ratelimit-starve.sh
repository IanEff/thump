#!/bin/bash
# thump chaos — rgw-ratelimit-starve.sh (targets rook-gce-k3s, kubeconfig ceph-gce)
#
# Attempts to force real RGW request failures (radosgw's "Aborted requests"
# perf counter, ceph_rgw_failed_req — the metric the ceph-rgw-availability
# Sloth SLO burns on) by throttling the traffic-generator's RGW user down
# to near-zero ops/min via `radosgw-admin ratelimit`, rather than by
# degrading the network or the backing pool.
#
# CONFIRMED DEAD END, kept as a documented one (thump-running-notes.md
# 2026-07-13 part 3, mechanism 3): `ratelimit enable` reports success but
# was never actually enforced on this deployment — RGW's own request log
# kept showing http_status=200 on every PUT regardless. Root cause not
# chased down (suspected realm/period propagation; `radosgw-admin period
# update --commit` failed outright with "failed to load realm"). The
# mechanism that actually works is rgw-user-suspend.sh.
#
# Moved here from the rig repo (rook-gce-k3s/chaos/) — chaos scripts belong
# to thump, not the infra repo they target.
#
# Usage (same ceph-gce kubeconfig-context assumption as the other chaos
# scripts):
#   chaos/rgw-ratelimit-starve.sh           # starve: 1 read-op/min, 1 write-op/min
#   chaos/rgw-ratelimit-starve.sh restore   # disable the ratelimit
#
# The zonegroup/zone flags below are NOT optional — radosgw-admin's
# default zonegroup lookup ("default") doesn't match this cluster's real
# zonegroup name ("ceph-objectstore") and every command fails with
# "ERROR: incorrect zonegroup" without them. Found this out the hard way.
set -euo pipefail

NAMESPACE="rook-ceph"
ZONEGROUP="ceph-objectstore"
ZONE="ceph-objectstore"
ACTION="${1:-starve}"

_toolbox() { kubectl exec -n "$NAMESPACE" deploy/rook-ceph-tools -- "$@" --rgw-zonegroup="$ZONEGROUP" --rgw-zone="$ZONE"; }

# The traffic generator's RGW user is OBC-provisioned (ObjectBucketClaim),
# so its uid carries a random UUID suffix that changes every time the
# bucket/OBC is recreated — look it up live by access key rather than
# hardcoding it.
ACCESS_KEY="$(kubectl -n default get secret traffic-generator-bucket -o jsonpath='{.data.AWS_ACCESS_KEY_ID}' | base64 -d)"
UID_LOOKUP="$(_toolbox radosgw-admin user info --access-key="$ACCESS_KEY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["user_id"])')"

case "$ACTION" in
  starve)
    echo "Rate-limiting RGW user ${UID_LOOKUP} to 1 read-op/min, 1 write-op/min..."
    _toolbox radosgw-admin ratelimit set --ratelimit-scope=user --uid="$UID_LOOKUP" \
      --max-read-ops=1 --max-write-ops=1
    _toolbox radosgw-admin ratelimit enable --ratelimit-scope=user --uid="$UID_LOOKUP"
    echo "--- after ---"
    _toolbox radosgw-admin ratelimit get --ratelimit-scope=user --uid="$UID_LOOKUP"
    ;;
  restore)
    echo "Disabling the ratelimit on ${UID_LOOKUP}..."
    _toolbox radosgw-admin ratelimit disable --ratelimit-scope=user --uid="$UID_LOOKUP"
    echo "--- after ---"
    _toolbox radosgw-admin ratelimit get --ratelimit-scope=user --uid="$UID_LOOKUP"
    ;;
  *)
    echo "Usage: $0 [starve|restore]" >&2
    exit 1
    ;;
esac
