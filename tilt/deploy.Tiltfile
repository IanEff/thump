# tilt/deploy.Tiltfile — chart rendering, manifest filtering, service wiring.
#
# load()'d by the root Tiltfile after tilt/build.Tiltfile. Exports setup()
# which renders the Helm chart, filters dangerous manifests, and wires all
# k8s_resource dependency chains.

def setup(cluster, cluster_name, domain_values, domains):
    """Renders the chart, filters manifests, and wires k8s_resource deps.

    Args:
        cluster: the resolved CLUSTERS[cluster_name] dict.
        cluster_name: "dev" or "thump-test".
        domain_values: the parsed values file (from infra.setup()).
        domains: domain_values["domains"] (from infra.setup()).
    """

    # Unlike a bare `helm template` (what chart-lint runs, and why that path
    # needs its own CRD-blind kubeconform pass), Tilt's built-in helm() always
    # passes --include-crds, so crds/approvalrequest.yaml is already in this
    # k8s_yaml() call — a separate manual k8s_yaml() of the same file
    # duplicated the CRD and broke `tilt up` (2026-07-31). helm() watches the
    # whole chart dir, so crds/ edits still live-reload same as templates/.
    #
    # namespace= is load-bearing, not decoration. Without it `helm template`
    # resolves `.Release.Namespace` to "default", and three templates key off
    # it: rbac-approver, rbac-hiss-approvalrequests, and secret.yaml. Every
    # other template in the chart hardcodes `namespace: thump`, so 28 of 31
    # objects landed correctly and only the RBAC quietly went to the `default`
    # namespace — where it grants nothing, since hiss's ServiceAccount lives
    # in `thump`. `kubectl auth can-i create approvalrequests.thump.dev`
    # answering `no` on a cluster whose CRD exists and whose Role exists
    # (elsewhere) is the signature. Diagnosed 2026-07-31 after two sessions
    # blamed it on Tilt re-triggering.
    rendered = helm("deploy/chart/thump", namespace = "thump", values = [cluster["values"]])

    # secret.yaml's thump-seal and nats-js-key Secrets are guarded by
    # `{{- if not (lookup ...) }}` so a real `helm install` mints them once
    # and leaves them alone. `lookup` always reads empty under `helm template`
    # (what helm() above runs), so left in this manifest set, that guard is
    # inert: every Tiltfile reload re-renders a *freshly random* key and
    # k8s_yaml applies it over whatever's already live. That's what broke
    # thump-test 2026-07-31 — an unrelated chart edit (OTel env vars)
    # triggered a reload mid-session, silently rotated nats-js-key's value,
    # and the running NATS pod could no longer decrypt its own on-disk
    # JetStream store ("unable to recover keys" / stream "could not be
    # recovered"). Strip both Secret objects out of what k8s_yaml ever sees —
    # thump-seal-secret and thump-nats-js-key-secret in tilt/infra.Tiltfile
    # (create-if-absent) become the *only* thing allowed to touch them under
    # Tilt, enforcing the same once-only intent the chart's lookup guard has
    # for a real `helm install`, by manifest filtering instead of a guard Tilt
    # can't evaluate.
    _, rendered = filter_yaml(rendered, kind = "Secret", name = "thump-seal")
    _, rendered = filter_yaml(rendered, kind = "Secret", name = "nats-js-key")

    # Namespace/thump comes out for the same reason, and it is the big one.
    #
    # The chart owns its own namespace (templates/namespace.yaml), correct for
    # a real `helm install`. Under Tilt it means `Namespace/thump` is an
    # object in the managed set, and Tilt garbage-collects the previous set on
    # any reload that changes it. Deleting a Namespace is a *cascading* delete
    # of everything inside it, so a one-line chart edit mid-session tears down
    # ConfigMaps, Secrets, RBAC, and the `data-nats-0` PVC — and then Tilt
    # re-applies into a namespace that is still Terminating, which the API
    # server refuses with "unable to create new content in namespace thump
    # because it is being terminated". Observed verbatim in Tilt's own log,
    # 2026-07-31:
    #
    #     Beginning garbage collecting Kubernetes objects
    #     Deleting kubernetes objects:
    #     → Namespace/thump
    #
    # This is the mechanism behind three separate sessions' worth of symptoms
    # that each got blamed on something else: the "clobbered" nats-js-key that
    # left JetStream unable to decrypt its own store (the Secret was
    # cascade-deleted with the namespace, then re-minted against a PVC that
    # outlived it), the ConfigMaps and Roles "a per-resource trigger pass
    # missed" (they were deleted, not skipped), and the namespace found stuck
    # Terminating "for reasons not established". None of it needed a finalizer
    # to explain it.
    #
    # ENSURE_NS in tilt/infra.Tiltfile already creates the namespace
    # create-if-absent, so nothing is lost by taking it away from Tilt — and
    # once no Tilt operation can delete it, none of the above can recur.
    _, rendered = filter_yaml(rendered, kind = "Namespace", name = "thump")
    k8s_yaml(rendered)

    # Pulls the acme/otel-demo RBAC and the ServiceMonitor out of Tilt's
    # "uncategorized" bucket (see the acme-namespace/otel-demo-namespace/
    # servicemonitor-crd comments in tilt/infra.Tiltfile) so each can carry
    # resource_deps on the local_resource that ensures its target
    # namespace/CRD exists, instead of relying on Tilt's
    # undocumented-as-guaranteed "local resources run first" default ordering.
    # Guarded the same way as their local_resource — an objects= selector for
    # an object absent from `rendered` fails outright.
    if domains.get("acme", {}).get("enabled", False):
        k8s_resource(
            new_name = "acme-rbac",
            objects = [
                "thump-acme-actuate:role", "thump-acme-actuate:rolebinding",
                "thump-acme-read:role", "thump-acme-read:rolebinding",
            ],
            resource_deps = ["acme-namespace"],
            labels = ["infra"],
        )
    if domains.get("otelDemo", {}).get("enabled", False):
        k8s_resource(
            new_name = "otel-demo-rbac",
            objects = [
                "thump-otel-demo-actuate:role", "thump-otel-demo-actuate:rolebinding",
                "thump-otel-demo-read:role", "thump-otel-demo-read:rolebinding",
            ],
            resource_deps = ["otel-demo-namespace"],
            labels = ["infra"],
        )
    if domain_values.get("serviceMonitor", {}).get("enabled", False):
        k8s_resource(
            new_name = "thump-servicemonitor",
            objects = ["thump:servicemonitor:thump"],
            resource_deps = ["servicemonitor-crd"],
            labels = ["infra"],
        )

    # Bring up NATS first — the beats dial it on boot; bring it up (and Ready)
    # before them. On dev, nats-tls (certificates.yaml) can't issue until
    # dev-substrate's cert-manager + thump-ca ClusterIssuer exist — without
    # this dep, NATS's StatefulSet pod is stuck waiting on a Secret volume
    # that never appears.
    #
    # objects=[...]: thump-nats:configmap and nats-tls:certificate have no
    # owner reference to the nats StatefulSet Tilt can discover on its own, so
    # without this they land in the "uncategorized" bucket alongside every
    # other orphan object in the chart — proven to bite this exact pair live
    # 2026-08-11: an unrelated RBAC object failing on "namespace not found"
    # took thump-nats:configmap and nats-tls:certificate down with it,
    # permanently (the uncategorized bucket retries as one unit, so one object
    # that can never succeed on this profile blocks every object sharing the
    # bucket, forever — not just delayed). Folding both into the "nats"
    # resource here means they apply exactly when the StatefulSet does, gated
    # on the same nats_deps, immune to whatever else is or isn't wrong
    # elsewhere in the uncategorized set.
    nats_deps = ["thump-nats-js-key-secret"]
    if cluster_name == "dev":
        nats_deps.append("dev-substrate")
    k8s_resource(
        "nats",
        objects = ["thump-nats:configmap", "nats-tls:certificate"],
        labels = ["broker"],
        resource_deps = nats_deps,
    )

    # thump-bootstrap: a Helm pre-install/pre-upgrade hook Job
    # (job-bootstrap.yaml) that calls broker.ConnectAndEnsure against NATS.
    # Under a real `helm install` the hook-weight annotations order it
    # relative to other hooks, but Tilt's helm() only templates the chart — it
    # doesn't run Helm's hook lifecycle, so nothing here stops this Job from
    # racing NATS. Without this resource_deps, it dialed a NATS Service with
    # no pod behind it yet and burned through backoffLimit before NATS ever
    # came up (2026-07-29 incident).
    k8s_resource("thump-bootstrap", labels = ["broker"], resource_deps = ["nats", "thump-s3-secret"])

    for beat in ["rattle", "clank", "hiss", "thump"]:
        # thump-bootstrap creates the streams/consumers beat.AwaitConsumers
        # binds to on startup (clank, hiss, thump all call it) — that call is
        # a single lookup with no retry, so racing ahead of bootstrap means an
        # immediate exit(1) and a k8s restart, which is what "fixes itself" a
        # restart or two later without this dep (2026-07-29 incident, part 2:
        # same race one edge further down).
        deps = [
            "nats",
            "thump-bootstrap",
            "thump-s3-secret",
        ]  # ← every beat ships its WAL to S3
        if beat == "clank":
            deps.append("thump-anthropic-secret")  # ← or until its Secret does
        if beat in ("clank", "hiss"):
            deps.append("thump-seal-secret")  # ← only these two mount seal.secretName
        if beat == "hiss":
            deps.append("approvalrequests.thump.dev")

        k8s_resource(
            beat,
            labels = ["machine"],
            resource_deps = deps,
            trigger_mode = TRIGGER_MODE_MANUAL,  # same "you decide when it wakes" posture (W0-4)
        )
