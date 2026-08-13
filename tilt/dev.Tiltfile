# tilt/dev.Tiltfile — dev-only resources (cluster_name == "dev").
#
# load()'d by the root Tiltfile only when cluster_name == "dev". Exports
# setup() which registers s3mock and dev convenience port-forwards.

def setup(cluster):
    """Registers s3mock and dev-only port-forwards.

    Args:
        cluster: the resolved CLUSTERS["dev"] dict.
    """

    # s3mock: adobe/s3mock replaces MinIO as this profile's WAL/transcript
    # durability layer (2026-08-11 — MinIO was this PR's original choice, but
    # Ian had already settled on S3Mock for local dev on 2026-07-07; lighter
    # on laptop memory, and it needs no bucket-creation Job — the manifest's
    # COM_ADOBE_TESTING_S3MOCK_STORE_INITIAL_BUCKETS env var does that on
    # startup). Full manifest: deploy/dev/manifests/s3mock.yaml.
    #
    # No resource_deps: unlike MinIO (installed by dev-substrate's Helm
    # chain), S3Mock needs nothing dev-substrate provides — it comes up in
    # parallel with dev-substrate's ~10-15 minute run.
    #
    # Every beat dials S3Mock's plain HTTP port (9090), not its bundled
    # self-signed-cert HTTPS port (9191, still exposed for a convenience curl
    # from a debug pod). Declared plaintext rather than TLS with peer
    # verification skipped: I-16 (docs/invariants.md) forbids
    # InsecureSkipVerify categorically, and already accepts exactly this shape
    # for Prometheus/Loki — a vendored dev backend that doesn't serve real
    # TLS, riding node-to-node Cilium WireGuard instead.
    # internal/config/config.go's RequireURL("S3_ENDPOINT", "http", "https")
    # is where that's enforced.
    k8s_yaml("deploy/dev/manifests/s3mock.yaml")
    k8s_resource("s3mock", labels = ["broker"])

    k8s_yaml("deploy/dev/manifests/acme.yaml")
    k8s_resource("acme-api", labels = ["acme"])

    # dev-only convenience port-forwards. The Services below are installed by
    # deploy/dev/bootstrap.sh's own helm releases, not by this Tiltfile's
    # k8s_yaml(rendered) — Tilt has no object-graph knowledge of them to
    # attach a k8s_resource() port_forward to, so each gets its own
    # local_resource running `kubectl port-forward` as a supervised serve_cmd
    # instead. No Gateway API/Ingress in this env
    # (deploy/dev/values/cilium.yaml's gatewayAPI.enabled: false) — this is
    # the whole "reach the UI" story.
    def _dev_port_forward(name, service, namespace, local_port, remote_port):
        local_resource(
            name,
            serve_cmd = "kubectl --context " + cluster["context"]
            + " -n " + namespace + " port-forward svc/" + service
            + " " + str(local_port) + ":" + str(remote_port),
            resource_deps = ["dev-substrate"],
            labels = ["ui"],
        )

    _dev_port_forward("grafana-ui", "grafana", "monitoring", 3000, 80)
    _dev_port_forward("prometheus-ui", "prometheus-kube-prometheus-prometheus", "monitoring", 9090, 9090)
    _dev_port_forward("hubble-ui", "hubble-ui", "kube-system", 12000, 80)
    _dev_port_forward("otel-demo-ui", "frontend-proxy", "otel-demo", 8080, 8080)
    _dev_port_forward("nats-port-forward", "nats", "thump", 4222, 4222)
