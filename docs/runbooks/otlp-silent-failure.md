# Runbook: OTLP silent-retry (dead trace pipeline, no error)

## Failure mode

`internal/beat/trace.go`'s `newOTLPExporter` dials the collector over gRPC
with `otlptracegrpc`. If `TLS_CA_FILE` is wrong, stale, or the collector's
cert was rotated without updating it, the exporter sees the handshake fail as
`codes.Unavailable` — and `otlptracegrpc`'s default retry policy treats
`Unavailable` as retryable. The SDK just keeps retrying forever, silently.
There is no log line at the failure point, no error returned to the beat's
`Main`, and no crash. The pod sits `1/1 Running`, healthy by every check that
exists today, while every span it ever emits is dropped on the floor.

This is a known, declared gap — the OTLP row in `../design-decisions.md`'s D-13
sweep is the record of it. Fixing it needs a `/readyz`-adjacent liveness check or an
OTel exporter health callback (SDK-side visibility into exporter retry
state), both bigger than this runbook. Until then, diagnosis is manual.

## Symptom

- A gap in Tempo (or whatever OTLP backend the cluster targets) for a beat
  that is otherwise operating normally — proposals, decisions, actuations
  all flowing, just no spans.
- No corresponding error in the beat's own `slog` output.
- The pod's readiness/liveness probes pass throughout.

## Check

Confirm the collector connection is actually exporting, not just running:

```
kubectl logs -n thump -l app.kubernetes.io/component=clank --tail=200 | grep -c 'exporting spans'
```

(swap `component=clank` for `rattle`/`hiss`/`thump`/`trim` as needed). A
healthy exporter logs a debug line per batch shipped; a count of `0` over a
window where the beat has clearly been doing work is the tell — the absence
of the line is the finding, not any single log message.

Cross-check against the collector side: does the collector's own ingest
metric show received spans from this beat's pod in the same window? If the
beat believes it's exporting but the collector shows nothing arriving,
that's the same failure from the other end.

## Fix

1. Verify `TLS_CA_FILE` on the beat's deployment matches the CA that
   actually signed the OTLP collector's serving cert — a rotation on one
   side without the other is the most common cause.
2. If the CA has rotated, update the mounted secret/configmap the beat reads
   `TLS_CA_FILE` from and let the deployment roll, or `kubectl rollout
   restart` the beat to force it to reconnect with the corrected CA.
3. Re-run the check above after the bounce — the exporting-spans line
   reappearing confirms the fix; a probe restart alone does not, since the
   probes never caught the failure in the first place.
