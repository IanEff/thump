# tilt/thump_test.Tiltfile — thump-test-only resources.
#
# load()'d by the root Tiltfile only when cluster_name == "thump-test".
# Exports setup() which registers the notify-echo fixture.

def setup():
    """Registers the thump-notify-echo Deployment and Service."""

    # thump-notify-echo: a Slack-webhook stand-in for verifying the notify
    # wire format before a live session (phase-af-cut-not-clobber.md Step 0c).
    # slack.Webhook (internal/notify/slack/slack.go) posts a POST
    # {"text": ...} to whatever URL it's given and only cares about the 2xx it
    # gets back — nothing about the wire is Slack-specific, so an echo
    # receiver proves the contract without a real webhook URL.
    # mendhak/http-https-echo logs every request (headers + body) to stdout,
    # so `kubectl logs deploy/thump-notify-echo -n thump` shows the digest
    # slack.digest() rendered, no debugger needed.
    #
    # Raw k8s_yaml(), not part of the chart — this is dev-session scaffolding,
    # not something a real `helm install` should ever carry. The "thump"
    # namespace already exists by this point (tilt/infra.Tiltfile's local()
    # call runs before any k8s_yaml), so no resource_deps is needed.
    k8s_yaml(blob("""
apiVersion: apps/v1
kind: Deployment
metadata:
  name: thump-notify-echo
  namespace: thump
spec:
  replicas: 1
  selector:
    matchLabels: {app: thump-notify-echo}
  template:
    metadata:
      labels: {app: thump-notify-echo}
    spec:
      containers:
        - name: echo
          image: mendhak/http-https-echo:31
          ports:
            - containerPort: 8080
          env:
            - name: HTTP_PORT
              value: "8080"
---
apiVersion: v1
kind: Service
metadata:
  name: thump-notify-echo
  namespace: thump
spec:
  selector: {app: thump-notify-echo}
  ports:
    - port: 8080
      targetPort: 8080
"""))
    k8s_resource("thump-notify-echo", labels = ["infra"])
