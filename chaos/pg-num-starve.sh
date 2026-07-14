#!/bin/bash
# thump chaos — pg-num-starve.sh (targets rook-gce-k3s, kubeconfig ceph-gce)
#
# Starves the RGW data pool backing the s3-traffic-generator's bucket down to
# a single PG, to see how RGW/OSDs behave when a pool is badly undersized on
# PGs — a different failure shape than the pod/disk/network faults
# chaos-mesh already injects (rgw-network-delay.yaml, rgw-network-loss.yaml,
# osd-chaos.yaml). This is D3(a)'s mechanism (ceph-osd-latency) and one of
# the seven mechanisms tried for D2/E3 (thump-running-notes.md 2026-07-13
# part 3) — PG-starve alone only ever produces latency, never moves
# ceph_rgw_failed_req, which is exactly why it's a candidate for exercising
# the new raw-latency ceph-rgw-saturation SLO instead.
#
# Moved here from the rig repo (rook-gce-k3s/chaos/) — chaos scripts belong
# to thump, not the infra repo they target, so all chaos tooling lives in
# one place regardless of which cluster/profile it's aimed at.
#
# The pool is `bulk: true` (applications/rook/storage/object-store.yaml)'s
# dataPool, so the mgr autoscaler's own target for it is much higher than 1
# (256 as of this writing — see `ceph osd pool autoscale-status`). Setting
# pg_num=1 with the autoscaler still on gets silently reverted within a few
# minutes, so `starve` turns the autoscaler off first; `restore` hands it
# back and lets the autoscaler grow the pool back out on its own.
#
# Usage (same ceph-gce kubeconfig-context assumption as the other chaos
# scripts — no --context flag here on purpose):
#   chaos/pg-num-starve.sh           # starve: autoscaler off, pg_num -> 1
#   chaos/pg-num-starve.sh restore   # autoscaler back on
#
# Override POOL= if the traffic generator ever points at a different
# CephObjectStore's data pool than the default single-object-store setup.
set -euo pipefail

NAMESPACE="rook-ceph"
POOL="${POOL:-ceph-objectstore.rgw.buckets.data}"
ACTION="${1:-starve}"

_toolbox() { kubectl exec -n "$NAMESPACE" deploy/rook-ceph-tools -- "$@"; }

if ! _toolbox ceph osd pool ls | grep -qx "$POOL"; then
    echo "Pool '$POOL' not found. Pools present:" >&2
    _toolbox ceph osd pool ls >&2
    exit 1
fi

case "$ACTION" in
  starve)
    echo "--- before ---"
    _toolbox ceph osd pool autoscale-status

    echo "Turning off the autoscaler on ${POOL} (bulk pool wants 256 PGs and will fight a manual pg_num otherwise)..."
    _toolbox ceph osd pool set "$POOL" pg_autoscale_mode off

    echo "Setting pg_num=1 on ${POOL}..."
    _toolbox ceph osd pool set "$POOL" pg_num 1

    echo "--- after (merge is async — pg_num may still be draining toward 1) ---"
    _toolbox ceph osd pool autoscale-status
    echo "Watch it land with: kubectl exec -n ${NAMESPACE} deploy/rook-ceph-tools -- ceph osd pool ls detail | grep ${POOL}"
    ;;
  restore)
    echo "Turning the autoscaler back on for ${POOL}..."
    _toolbox ceph osd pool set "$POOL" pg_autoscale_mode on

    echo "--- after (autoscaler will grow pg_num back toward its own target) ---"
    _toolbox ceph osd pool autoscale-status
    ;;
  *)
    echo "Usage: $0 [starve|restore]" >&2
    exit 1
    ;;
esac
