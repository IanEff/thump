# tilt/infra.Tiltfile — namespace, secrets, domain/CRD gates.
#
# load()'d by the root Tiltfile after tilt/lib.Tiltfile. Exports setup()
# which creates all infrastructure resources. Returns a struct with
# domain_values and domains for use by tilt/deploy.Tiltfile.

def setup(cluster, cluster_name, h):
    """Creates namespace, secrets, domain/CRD gate resources.

    Args:
        cluster: the resolved CLUSTERS[cluster_name] dict.
        cluster_name: "dev" or "thump-test".
        h: the helpers struct from make_helpers().

    Returns:
        struct with domain_values, domains for deploy.Tiltfile.
    """

    # dev-substrate (cluster=dev only): everything deploy/chart/thump assumes
    # is already on the cluster — cert-manager+ClusterIssuer, prometheus-
    # operator CRDs, kube-prometheus-stack, Cilium, Loki/Promtail,
    # Tempo/otel-collector, MinIO, the SLO PrometheusRule, the OTel demo — on
    # thump-test, provisioning is a separate rig repo's job, done long before
    # `tilt up` ever runs. The dev profile has no rig repo, so
    # deploy/dev/bootstrap.sh does it here instead, as the one thing every
    # other dev-cluster resource depends on transitively through
    # nats/thump-s3-secret below.
    #
    # guarded() (not a plain local_resource cmd): bootstrap.sh's very first
    # helm call needs a reachable API server exactly like every other
    # kubectl/helm call in this file, and its ~10-15 minute runtime means a
    # transient blip is expensive to just fail outright on — same reasoning as
    # every other guarded() call here, scaled up.
    if cluster_name == "dev":
        local_resource(
            "dev-substrate",
            cmd = h.guarded("dev-substrate", "./deploy/dev/bootstrap.sh", attempts = 1),
            deps = ["deploy/dev/bootstrap.sh", "deploy/dev/values"],
            labels = ["substrate"],
        )

    # thump-anthropic-secret: clank's Secret is meant to pre-exist out-of-band
    # (the lab's SOPS flow owns it in prod — see deploy/chart/thump/templates/
    # secret.yaml's comment). Under Tilt there's no SOPS flow, so this is the
    # dev stand-in: read the key from a gitignored .env
    # (ANTHROPIC_API_KEY="...") and apply the Secret directly via kubectl,
    # never through anthropic.create/apiKey in a values file (that stays a
    # `--set`-only escape hatch, per the chart's own warning against committing
    # a plaintext key). --dry-run=client -o yaml | apply, not `create`, so
    # re-running this after rotating the key in .env updates the Secret instead
    # of no-op'ing.
    h.kubectl_local(
        "thump-anthropic-secret",
        'set -a; source .env 2>/dev/null || { echo ".env not found at repo root — expected ANTHROPIC_API_KEY=\\"...\\"" >&2; exit 1; }; set +a; '
        + '[ -n "$ANTHROPIC_API_KEY" ] || { echo "ANTHROPIC_API_KEY not set in .env" >&2; exit 1; }; '
        + h.ENSURE_NS
        + " && kubectl --context "
        + cluster["context"]
        + " -n thump create secret generic thump-anthropic "
        + '--from-literal=api-key="$ANTHROPIC_API_KEY" --dry-run=client -o yaml | kubectl --context '
        + cluster["context"]
        + " apply -f -",
    )

    # thump-s3-secret: same posture as thump-anthropic-secret above — every
    # beat (not just clank) needs this once NATS_URL is set, since every
    # beat's broker path Require()s all four S3_* vars for its WAL shipper
    # (internal/config/config.go). Sourced from .env, never from a values
    # file, same reasoning as the Anthropic key.
    #
    # dev is the one exception: there's no out-of-band bucket to source .env
    # from, so this profile hardcodes the credentials adobe/s3mock accepts —
    # it doesn't check them at all (see the s3mock k8s_resource in
    # tilt/dev.Tiltfile) — against the S3Mock instance this Tiltfile stands up
    # directly, instead of requiring the dev to hand-populate .env for a store
    # this repo already provisions for him. ANTHROPIC_API_KEY above stays the
    # one thing he does supply — that's the point of this env, not an
    # oversight. endpoint is http:// against S3Mock's plain port 9090, not its
    # bundled-self-signed-cert 9191: internal/config/config.go's
    # RequireURL("S3_ENDPOINT", "http", "https") accepts either scheme for
    # exactly this reason — declared plaintext for a vendored dev backend that
    # doesn't serve real TLS, the same shape I-16 already accepts for
    # Prometheus/Loki (docs/invariants.md), rather than an unauthenticated TLS
    # session dressed as a secure one. See the s3mock k8s_resource in
    # tilt/dev.Tiltfile for the port split.
    if cluster_name == "dev":
        s3_body = (
            h.ENSURE_NS
            + " && kubectl --context "
            + cluster["context"]
            + " -n thump create secret generic thump-s3 "
            + '--from-literal=endpoint="http://s3mock.thump.svc.cluster.local:9090" --from-literal=bucket="thump-wal" '
            + '--from-literal=access-key="thump-dev" --from-literal=secret-key="thump-dev-secret" '
            + "--dry-run=client -o yaml | kubectl --context "
            + cluster["context"]
            + " apply -f -"
        )
        s3_deps = ["s3mock"]
    else:
        s3_body = (
            'set -a; source .env 2>/dev/null || { echo ".env not found at repo root — expected S3_ENDPOINT/S3_BUCKET/S3_ACCESS_KEY/S3_SECRET_KEY" >&2; exit 1; }; set +a; '
            + "for v in S3_ENDPOINT S3_BUCKET S3_ACCESS_KEY S3_SECRET_KEY; do "
            + '  [ -n "${!v}" ] || { echo "$v not set in .env" >&2; exit 1; }; '
            + "done; "
            + h.ENSURE_NS
            + " && kubectl --context "
            + cluster["context"]
            + " -n thump create secret generic thump-s3 "
            + '--from-literal=endpoint="$S3_ENDPOINT" --from-literal=bucket="$S3_BUCKET" '
            + '--from-literal=access-key="$S3_ACCESS_KEY" --from-literal=secret-key="$S3_SECRET_KEY" '
            + "--dry-run=client -o yaml | kubectl --context "
            + cluster["context"]
            + " apply -f -"
        )
        s3_deps = []
    h.kubectl_local("thump-s3-secret", s3_body, resource_deps = s3_deps)

    # thump-seal-secret / thump-nats-js-key-secret: unlike
    # thump-anthropic-secret and thump-s3-secret above, these two keys are
    # pure internal material — no external system issues them, so there's
    # nothing to source from .env. The chart's own secret.yaml self-provisions
    # them on a real `helm install` via a `lookup`-guarded block, but `lookup`
    # always reads empty under `helm template` (what Tilt's helm() runs) — the
    # k8s_yaml() call in tilt/deploy.Tiltfile filter_yaml()s both Secret
    # objects out of the chart's rendered manifests entirely, so that inert
    # guard never gets a chance to matter under Tilt. These local_resources
    # are the *only* thing that ever creates or touches either secret in a Tilt
    # session: create-if-absent, never touch an existing one (unlike
    # thump-s3-secret's dry-run-apply, which is meant to re-sync every run) —
    # `kubectl get ... || kubectl create ...`, not `--dry-run=client -o yaml |
    # apply`.
    #
    # `get || create` is the obvious shape for create-if-absent and it is the
    # wrong one here: a `get` that fails because the API was unreachable is
    # indistinguishable from one that fails because the secret is absent, and
    # the `||` branch answers both by minting a fresh random key. On
    # thump-seal that orphans every sealed WAL segment; on nats-js-key it
    # orphans the on-disk JetStream store, which is exactly the failure
    # recorded on 2026-07-31. So: only a literal NotFound authorises a create.
    # Any other error returns non-zero so kubectl_local retries it, and says
    # out loud that it declined to mint a key rather than doing it quietly.
    def _ensure_key_secret(resource, secret):
        kubectl = "kubectl --context " + cluster["context"]
        h.kubectl_local(
            resource,
            h.ENSURE_NS
            + " && out=$("
            + kubectl
            + " -n thump get secret "
            + secret
            + ' 2>&1); if [ $? -eq 0 ]; then exit 0; fi; case "$out" in *NotFound*) '
            + kubectl
            + " -n thump create secret generic "
            + secret
            + ' --from-literal=key="$(openssl rand -base64 32)" ;; *) echo "thump: cannot tell whether secret '
            + secret
            + ' exists, refusing to mint a replacement key over data encrypted under the old one: $out" >&2; false ;; esac',
        )

    _ensure_key_secret("thump-seal-secret", "thump-seal")
    _ensure_key_secret("thump-nats-js-key-secret", "nats-js-key")

    # The namespace is filtered out of the chart's manifests (see the
    # filter_yaml block in tilt/deploy.Tiltfile for why), so something else
    # has to create it — and it has to exist before Tilt applies a single
    # namespaced object. local() runs during Tiltfile evaluation, ahead of
    # every apply and every local_resource, which is the ordering guarantee a
    # local_resource can't give: Tilt's `uncategorized` bucket isn't
    # addressable by k8s_resource(), so it can't be told to wait. Idempotent,
    # so a reload re-runs it as a no-op rather than a teardown.
    local(h.guarded("thump-namespace", h.ENSURE_NS), quiet = True, echo_off = True)

    # domains.{acme,otelDemo}.enabled (values.yaml's own comment: "off by
    # default so clusters without these namespaces still deploy clean") gates
    # RBAC in rbac-acme-actuate.yaml / rbac-otel-demo-actuate.yaml that
    # targets a fixed namespace outside this chart's own manifests — acme's
    # and otel-demo's workloads are provisioned by the thump-test rig repo,
    # not by this Tiltfile. On a freshly-created thump-test cluster that
    # provisioning is a separate, unordered process: the namespace can still
    # be missing when the RBAC applies.
    #
    # These run as local_resources, not blocking local() — a `local()` here
    # would re-run on every Tiltfile reload (any chart/values edit) and a
    # slow/missing namespace would raise a Starlark traceback that kills the
    # whole `tilt up`. The acme-rbac / otel-demo-rbac k8s_resource() blocks in
    # tilt/deploy.Tiltfile pull the Role/RoleBinding pair out of Tilt's
    # "uncategorized" bucket and gate them on these via resource_deps, so a
    # missing namespace is a pending resource, not a batch-wide abort —
    # observed live 2026-08-03: thump-acme-actuate:role failed on "namespaces
    # \"acme\" not found" and took thump-nats:configmap and nats-tls:certificate
    # down with it, despite neither having anything to do with acme.
    domain_values = read_yaml(cluster["values"], default = {})
    domains = domain_values.get("domains", {})
    if domains.get("acme", {}).get("enabled", False):
        h.kubectl_local("acme-namespace", h.ensure_ns_cmd("acme"))
    if domains.get("otelDemo", {}).get("enabled", False):
        h.kubectl_local("otel-demo-namespace", h.ensure_ns_cmd("otel-demo"))

    # serviceMonitor.enabled gates templates/servicemonitor.yaml, which targets
    # monitoring.coreos.com/v1's ServiceMonitor kind — a CRD
    # kube-prometheus-stack owns. On a freshly-created cluster the CRD can
    # still be unregistered when the ServiceMonitor applies.
    #
    # Runs as a local_resource, gated the same way as the namespace guards
    # above: the thump-servicemonitor k8s_resource() block in
    # tilt/deploy.Tiltfile pulls the ServiceMonitor object out of the
    # uncategorized bucket and gives it resource_deps=["servicemonitor-crd"],
    # so a slow CRD is a pending resource, not a Starlark traceback that kills
    # the whole session. Observed live 2026-08-07: kube-prometheus-stack's CRDs
    # landed ~2.5 minutes after tilt up's first apply — far longer than a
    # blocking eval-time local() can afford to sit through on every reload.
    def _ensure_crd_cmd(name, timeout_s = 60):
        kubectl = "kubectl --context " + cluster["context"]
        if cluster_name == "dev":
            missing_hint = "is dev-substrate (deploy/dev/bootstrap.sh) still installing kube-prometheus-stack?"
        else:
            missing_hint = "is the monitoring stack (kube-prometheus-stack) still installing? check the rig repo provisioning scripts"
        return (
            "waited=0; until "
            + kubectl
            + " get crd "
            + name
            + " >/dev/null 2>&1; do "
            + "waited=$((waited+5)); "
            + "if [ $waited -ge "
            + str(timeout_s)
            + " ]; then "
            + 'echo "thump: CRD '
            + name
            + " not found after ${waited}s — "
            + missing_hint
            + '" >&2; exit 1; '
            + "fi; sleep 5; done"
        )

    if domain_values.get("serviceMonitor", {}).get("enabled", False):
        # dev: the CRD comes from dev-substrate's own kube-prometheus-stack
        # install (a Tilt-managed local_resource, not an out-of-band rig
        # repo), so without this dep the two race — bootstrap.sh's Helm
        # install easily outlasts the 60s CRD-wait budget below, and
        # servicemonitor-crd loses every time on a cold cluster.
        crd_deps = ["dev-substrate"] if cluster_name == "dev" else []
        h.kubectl_local(
            "servicemonitor-crd",
            _ensure_crd_cmd("servicemonitors.monitoring.coreos.com"),
            resource_deps = crd_deps,
        )

    return struct(
        domain_values = domain_values,
        domains = domains,
    )
