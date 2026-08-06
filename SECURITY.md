# Security Policy

## Supported versions

thump is pre-1.0. `v0.1.0` is the only tag, and there is no
supported-version matrix behind it — every report is evaluated against the tip
of `main`. This grows a real table when there's more than one line to put in it.

## Reporting a vulnerability

Please don't open a public issue for a suspected vulnerability — a public
issue is a disclosure, not a report. Use GitHub's private vulnerability
reporting instead: **Security tab → "Report a vulnerability"** on this
repository. That opens a private advisory thread with the maintainer rather
than a public inbox to babysit.

This is a single-maintainer project — expect an initial response in a few
days, not an SLA.

## Why the catalog is the sharpest edge here

thump reasons about live incidents and can act on a real Kubernetes cluster,
so the interesting attack surface isn't the usual web-app list — it's the
config that decides what the engine is allowed to do.

**A change to `config/actions/catalog.yaml` is a change to the execution
surface, not just to the reasoning it feeds.** Every action in that file
carries an `execution` block, and one verb (`exec`) takes argv directly —
`command: [ceph, osd, set, noout]`, authored plainly in YAML. Whoever can
merge a change to that file can run a command in any pod thump's
ServiceAccount can reach. There is no Go code standing between the catalog
and the cluster for that verb; the file *is* the binding.

**What bounds that isn't the verb list — it's RBAC.** thump's ServiceAccount
is scoped per namespace (see `deploy/chart/thump/templates/rbac-*.yaml`),
every live action additionally requires an armed global kill switch
(`internal/thump/killswitch.go`) and a passing verdict from thump's
governance stage, and a disarmed switch blocks execution regardless of what
the catalog or the governance verdict say. A compiled allowlist of
(namespace, selector) pairs for the `exec` verb was considered and
deliberately not built — RBAC is already the enforced bound, and a compiled
allowlist is the first thing worth adding the day this project starts taking
catalog changes from contributors who aren't already trusted reviewers.

In practice: a PR touching `config/actions/`, `config/hiss/`, or
`internal/actuate` gets reviewed as an execution-surface change, whatever
else it claims to do. See `CONTRIBUTING.md` § "What gets reviewed hardest"
for the reviewer's side of this same rule.

## Transport and at-rest encryption

thump has ten network legs and five places it holds data down. Every leg is
either TLS negotiated by this process against a CA it was handed, or a
plaintext exception with its reason on record; every store either never
outlives the pod or is sealed before it leaves the process.

**thump builds its own `*tls.Config` in exactly one place.** `internal/tlsx`
is the only package that constructs one — `Client` verifies the peer and
presents this beat's leaf when it has one, `Server` requires and verifies a
client cert (`tls.RequireAndVerifyClientCert`, not the zero-value
`NoClientCert`). Both floor at `MinVersion: tls.VersionTLS13`. Certificates
are re-read from disk per handshake through `GetCertificate` /
`GetClientCertificate` callbacks, so rotation doesn't need a pod restart. A
tripwire (`TestOnlyTlsxBuildsATLSConfig`, `internal/httpx/tripwire_test.go`)
walks every non-test `.go` file for a `tls.Config{}` literal outside this
package and fails the build on a hit — the enforcement is a compile-time
check, not a review convention.

### The legs

| Leg | Carries | Protection |
|---|---|---|
| beat ↔ NATS JetStream | every boundary object — `Detection`, `ProposalSet`, `Governed`, `Outcome`, `Approval` | mTLS; each beat's client cert maps by SAN email to a NATS user whose publish grants **are** the one-producer-per-object rule — `hiss@thump.svc` is the only identity with publish on `thump.decisions`, `clank@thump.svc` cannot publish it at all |
| beat → S3/GCS | sealed WAL segments, sealed clank transcripts | HTTPS, refused at config load if the URL isn't `https://`; payload sealed independently (see **At rest**, below) |
| clank → Anthropic | the model conversation, evidence digests included | HTTPS, SDK default, public CA |
| rattle/clank/thump → Prometheus, clank → Loki | PromQL/logQL queries and results | **plaintext HTTP** — the declared exception; see below |
| beat → Tempo (OTLP/gRPC) | spans, including attributes at every seam | `https://` verifies against the rig's private CA via `tlsx.Client` + `otlptracegrpc.WithTLSCredentials`; `http://` stays an authored operator choice for a collector that doesn't serve TLS, recorded with its reason in `declaredPlaintext` (`internal/httpx/tripwire_test.go`) |
| Prometheus → beat `/metrics` | RED counters, `/healthz`, `/readyz` | TLS server cert via `tlsx.Server`; `ListenAndServeTLS("", "")` reads the cert from the callback above, not from re-read file paths |
| thump → kube-apiserver | `pods/exec`, merge-patch | TLS, CA-verified, ServiceAccount-token authenticated — client-go's own default |
| thump → Slack | the notification body | HTTPS by webhook URL, refused at config load otherwise |
| Ceph mon/OSD/RGW ↔ clients | the storage domain thump acts on | msgr2, provisioned in the rig repo, not this one |

**The one declared plaintext exception is Prometheus and Loki.** Both are
vendored third-party charts that don't terminate TLS on this rig, so `httpx`'s
client (`internal/httpx/httpx.go`) takes an optional `*tls.Config` and both
call sites (`internal/rattle/rattle.go`, `internal/clank/clank.go`) already
pass one — the day either backend serves TLS, turning it on is a values
change, not a code change. Until then, both legs are carried by Cilium
WireGuard at the node level; same-node pod-to-pod traffic is not covered by
that, and that gap is accepted rather than overlooked. `InsecureSkipVerify`
is never a declared exception of this kind — the tripwire refuses it
categorically, with its own error message, and no allowlist entry can silence
it.

**The distroless base image carries a CA bundle.** The production image ends
`FROM gcr.io/distroless/static-debian12:nonroot` (`Dockerfile`), not
`FROM scratch` — outbound HTTPS to Anthropic, GCS, and Slack verifies against
real system roots. Worth stating because it looks like something `scratch`
would have skipped, until you check the base.

### At rest

| Store | Holds | Protection |
|---|---|---|
| WAL active/sealed segments | every published boundary object, as JSON lines | plaintext on `emptyDir`, modes `0o600`/`0o750` — defended rather than fixed: nothing outlives the pod, and this is the copy `calipers` reads during an incident |
| NATS JetStream store | up to 48h of every subject | JetStream's native encryption at rest — `cipher: chachapoly` (ChaCha20-Poly1305), key from a `$JS_KEY` env var sourced from a Secret, never the ConfigMap (`deploy/chart/thump/templates/nats.yaml`, `secret.yaml`). Chart-wired; the live restart-and-`strings`-over-`/data` proof against the rig is still owed |
| GCS bucket | shipped WAL segments and clank transcripts | `AES-256-GCM` sealed in-process before `PutObject` (`internal/sealbox`), so the bucket holds ciphertext regardless of who can read it; bucket-side, `public_access_prevention: enforced` is provisioned in the rig repo |
| k3s datastore | `ANTHROPIC_API_KEY`, the S3 HMAC pair, the Slack webhook, the seal key, every beat's TLS private key | `secrets-encryption: true`, provisioned in the rig repo — a cluster-create setting, not a runtime flag |
| Ceph OSD block devices | objects the rig's traffic generator writes | declined, with reason: no `encryptedDevice`, superseded by msgr2 transport encryption for this threat model |

**Why the bucket is sealed client-side instead of left to Google-managed
keys.** A clank transcript carries `ToolCalls` and
`ToolResults` with call/result pairing (`internal/clank/store.go`), so a
reader of the bucket doesn't just see what the model said — they reconstruct
which piece of evidence answered which question, for every run. That's the
highest-value artifact this engine produces, and Google-managed encryption
means Google decrypts for anyone who can read the bucket: the confidentiality
boundary is GCS IAM, the same door cluster RBAC already has to defend.
Client-side sealing (`sealbox`, one AES-256-GCM seal per object, a fresh
96-bit `crypto/rand` nonce every call) puts a second, independent door in
front of the same room — the bucket's access path (an HMAC key pair, GCS IAM,
a `public_access_prevention` misconfiguration) is entirely separate from
cluster RBAC, so a bucket compromise on its own yields ciphertext. A
compromise of the k3s datastore is a different door again, and it's the one
`secrets-encryption` closes. `calipers unseal` reads a sealed object back for
debugging; sealing reaches nothing, decides nothing, writes nothing, so it
carries none of the catalog's execution-surface concerns above.

## Scope

In scope: this repository — `internal/`, `cmd/`, the Helm chart under
`deploy/`, and the authored catalog and policy config that ships with it.

Out of scope: the third-party services thump talks to (Prometheus, Loki,
NATS, the Anthropic API) — report vulnerabilities in those upstream, to their
own maintainers.
