#!/bin/bash
# thump chaos — rgw-user-suspend.sh (targets rook-gce-k3s, kubeconfig ceph-gce)
#
# Forces real RGW request failures (radosgw's "Aborted requests" perf
# counter, ceph_rgw_failed_req — the metric the ceph-rgw-availability
# Sloth SLO burns on) by suspending the traffic-generator's RGW user
# outright, rather than degrading the network, the backing pool, or
# trying (and failing) to get a rate limit to actually take effect.
#
# THE MECHANISM THAT WORKS (thump-running-notes.md 2026-07-13 part 3,
# mechanism 7): pg-num-starve.sh, rgw-network-delay.yaml/rgw-network-
# loss.yaml, and rgw-ratelimit-starve.sh were all tried first and none
# moved ceph_rgw_failed_req. Reading Ceph's own source (rgw_process.cc/
# rgw_rest.cc) settled why: l_rgw_failed_req only increments inside
# abort_early(), which only fires for PRE-EXECUTION pipeline rejections
# (bad auth, rate-limiting, a suspended user, handler-init failure) —
# never for an op that starts executing and then fails normally (ENOSPC,
# quota-exceeded 507, a killed connection). Suspending the user hits
# `s->user->get_info().suspended` inside process_request(),
# deterministically, every request, no propagation delay.
#
# This is what produced the D2/E3 keystone fixture
# (internal/clank/testdata/detections/rgw-degradation.yaml) — but note
# the mechanism is an auth/access-control failure, not literally a
# backend-slow-under-load one, which is why the new ceph-rgw-saturation
# SLO exists (see rgw-network-delay.yaml): this script proves
# dependency_saturation via ceph-rgw-availability; that SLO is meant to
# catch the *other* shape of the same failure class, the one this
# mechanism structurally cannot produce.
#
# Moved here from the rig repo (rook-gce-k3s/chaos/, bundled by mistake
# into an unrelated commit, df3600c "suspend kyverno for test") — chaos
# scripts belong to thump, not the infra repo they target.
#
# Usage (same ceph-gce kubeconfig-context assumption as the other chaos
# scripts):
#   chaos/rgw-user-suspend.sh           # starve: suspend the RGW user
#   chaos/rgw-user-suspend.sh restore   # restore: re-enable the user
#
# The zonegroup/zone flags below are NOT optional — radosgw-admin's
# default zonegroup lookup ("default") doesn't match this cluster's real
# zonegroup name ("ceph-objectstore") and every command fails with
# "ERROR: incorrect zonegroup" without them.
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
RGW_UID="$(_toolbox radosgw-admin user info --access-key="$ACCESS_KEY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["user_id"])')"

case "$ACTION" in
  starve)
    echo "Suspending RGW user ${RGW_UID}..."
    _toolbox radosgw-admin user suspend --uid="$RGW_UID"
    echo "--- after ---"
    _toolbox radosgw-admin user info --uid="$RGW_UID" | python3 -c 'import json,sys; print("suspended:", json.load(sys.stdin)["suspended"])'
    ;;
  restore)
    echo "Re-enabling RGW user ${RGW_UID}..."
    _toolbox radosgw-admin user enable --uid="$RGW_UID"
    echo "--- after ---"
    _toolbox radosgw-admin user info --uid="$RGW_UID" | python3 -c 'import json,sys; print("suspended:", json.load(sys.stdin)["suspended"])'
    ;;
  *)
    echo "Usage: $0 [starve|restore]" >&2
    exit 1
    ;;
esac
