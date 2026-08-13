# tilt/lib.Tiltfile — shared helper functions for the Tiltfile.
#
# load()'d by the root Tiltfile. Exports make_helpers(cluster, cluster_name)
# which returns a struct of bound closures — guarded(), kubectl_local(),
# ensure_ns_cmd(), and the ENSURE_NS constant.

def make_helpers(cluster, cluster_name):
    """Returns a struct of helper functions bound to the given cluster config."""

    # guarded: every local_resource drives kubectl against the target cluster,
    # and on thump-test that cluster's API server is reachable only through a
    # single `gcloud compute start-iap-tunnel` relay on localhost:6443. That
    # relay drops connections under the burst of concurrent requests a `tilt up`
    # produces — surfacing as `net/http: TLS handshake timeout` or `unexpected
    # EOF` from whichever kubectl happened to be in flight.
    #
    # Tilt never retries a failed local_resource. So a two-second blip leaves
    # the resource red and every k8s resource that depends on it `pending`
    # forever, with no failing pod and no rolling log to point at — the run
    # reads as hung rather than failed, and the usual reflex (delete the
    # namespace, re-trigger resources by name) rebuilds it half-applied.
    # Wrapping the body here is what stops a transport hiccup from costing a
    # whole session.
    #
    # Two guards, in order:
    #   1. Wait for the API to answer before running the body at all, so an
    #      unreachable cluster reports itself as one and names the tunnel.
    #   2. Retry the body. A genuine config error (.env missing, key unset)
    #      still fails on the first pass — those branches `exit 1`, which
    #      leaves the shell outright and never reaches the next iteration.
    def _guarded(name, body, attempts = 3):
        # attempts defaults to 3 (transport-blip retries against a flaky IAP
        # tunnel, the reason this wrapper exists at all). dev-substrate passes
        # 1: its body is a ~10-15 minute non-resumable script, so retrying it
        # after a real failure restarts from step 1 and can triple a genuine
        # error into a 45-minute red — the retry is the amplifier there, not
        # the cure.
        kubectl = "kubectl --context " + cluster["context"]
        if cluster_name == "dev":
            unreachable_hint = "is the k3d cluster up? (task dev:cluster)"
        else:
            unreachable_hint = "is the IAP tunnel up? (just tunnel, in the rig repo)"
        preflight = (
            "waited=0; "
            + "until "
            + kubectl
            + " get --raw /readyz >/dev/null 2>&1; do "
            + "waited=$((waited+2)); "
            + "if [ $waited -ge 60 ]; then "
            + 'echo "thump: API server for context '
            + cluster["context"]
            + " unreachable after ${waited}s — "
            + unreachable_hint
            + '" >&2; exit 1; '
            + "fi; sleep 2; done; "
        )
        if attempts == 1:
            return (
                "bash -c '"
                + preflight
                + "{ "
                + body
                + "; } || { "
                + 'echo "thump: '
                + name
                + ' failed — not retried, see above" >&2; exit 1; }'
                + "'"
            )
        attempt_list = " ".join([str(n) for n in range(1, attempts + 1)])
        return (
            "bash -c '"
            + "for attempt in "
            + attempt_list
            + "; do "
            + preflight
            + "{ "
            + body
            + "; } && exit 0; "
            + 'echo "thump: '
            + name
            + ' attempt $attempt failed (transport?), retrying in 5s" >&2; '
            + "sleep 5; done; "
            + 'echo "thump: '
            + name
            + ' failed '
            + str(attempts)
            + " times — this is a real error, not a blip\" >&2; exit 1"
            + "'"
        )

    def _kubectl_local(name, body, labels = ["infra"], resource_deps = []):
        local_resource(name, cmd = _guarded(name, body), labels = labels, resource_deps = resource_deps)

    # ensure_ns_cmd: every secret resource needs the namespace to exist first,
    # and create-namespace-if-absent is the same three lines each time.
    def _ensure_ns_cmd(name):
        return (
            "kubectl --context "
            + cluster["context"]
            + " create namespace "
            + name
            + " --dry-run=client -o yaml | kubectl --context "
            + cluster["context"]
            + " apply -f - >/dev/null"
        )

    return struct(
        guarded = _guarded,
        kubectl_local = _kubectl_local,
        ensure_ns_cmd = _ensure_ns_cmd,
        ENSURE_NS = _ensure_ns_cmd("thump"),
    )
