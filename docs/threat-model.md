# Threat model

Who can make this engine do something to a cluster, and what stops them.

This is the authority half of the security picture. The cryptography half — which legs are
TLS, what's sealed at rest, how to report a vulnerability — is [`../SECURITY.md`](../SECURITY.md).
The rules cited here are in [`invariants.md`](invariants.md); the decisions behind them are
in [`design-decisions.md`](design-decisions.md).

## Actors

| Actor | Can cause | Bounded by |
|---|---|---|
| **Catalog author** (merge on a profile's `config/<profile>/actions/catalog.yaml`) | Any command in any pod thump's ServiceAccount can reach. The widest authority in the system | ServiceAccount RBAC, the kill switch, hiss's policy — reviewed as an execution-surface change |
| **Policy author** (merge on a profile's `config/<profile>/hiss/policy.yaml`) | Lower confidence floors, raise the auto-fire band, remove a freeze window | Same review path; hiss refuses to start without a policy, so there is no silent default |
| **Operator** (`calipers`) | Approve a held action; force one past the risk gate | Approval only releases what hiss already conditionally granted. `force` is attributed, audited, rendered `forced` everywhere, and still kill-switch-gated |
| **Cluster admin** (RBAC on `ApprovalRequest`) | Approve a held action as an authenticated Kubernetes subject | `spec.decision` accepts `approve` and nothing else. The API server records the patch independently of this engine |
| **The model** (Anthropic API) | Choose which catalogued action to propose, and argue for it | It cannot leave the catalog, invent a magnitude, or grant itself permission. See below |
| **Reader of the object store** | Read every WAL segment and reasoning transcript ever shipped | Sealed with AES-256-GCM in-process before upload, so bucket access alone yields ciphertext |
| **Reader of etcd** | Read `ApprovalRequest` objects | Deliberately the only thing in etcd. Reasoning, evidence, and verdicts never go there — see D-14 |
| **Reader of the forge** (read access to a `maintenanceRelease` contract's GitOps repo) | Read the full rendered `Set` for any release: subject identifiers, losing candidates, confidence, citations | Nothing in this engine — bounded only by the repo's own visibility, which is public on the rig's own test repo. See D-26 |

## The catalog is the sharpest edge

thump reasons about live incidents and can act on a real Kubernetes cluster, so the
interesting attack surface isn't the usual web-app list — it's the config that decides what
the engine is allowed to do.

**A change to any profile's `config/<profile>/actions/catalog.yaml` is a change to the
execution surface, not just to the reasoning it feeds.** (`config/actions/` is the base
copy, unread by anything deployed; `config/dev/actions/` and `config/thump-test/actions/`
are what each profile's `thump-actions` ConfigMap actually binds —
`deploy/chart/thump/templates/configmap-actions.yaml`.) Every action in that file carries
an `execution` block, and one verb (`exec`) takes argv directly —
`command: [ceph, osd, set, noout]`, authored plainly in YAML. Whoever can merge a change to
that file can run a command in any pod thump's ServiceAccount can reach. There is no Go
code standing between the catalog and the cluster for that verb; the file *is* the binding.

**What bounds that isn't the verb list — it's RBAC.** thump's ServiceAccount is scoped per
namespace (`deploy/chart/thump/templates/rbac-*.yaml`), every live action additionally
requires an armed global kill switch (`internal/thump/killswitch.go`) and a passing verdict
from governance, and a disarmed switch blocks execution regardless of what the catalog or
the verdict say. A compiled allowlist of (namespace, selector) pairs for the `exec` verb was
considered and deliberately not built — RBAC is already the enforced bound, and the
allowlist is the first thing worth adding the day this project starts taking catalog changes
from contributors who aren't already trusted reviewers.

In practice: a PR touching `config/*/actions/`, `config/*/hiss/`, or `internal/actuate` gets
reviewed as an execution-surface change, whatever else it claims to do. `CONTRIBUTING.md`
§ "What gets reviewed hardest" is the reviewer's side of the same rule.

## The model is untrusted input

An LLM reads attacker-influenceable text — log lines, alert annotations, pod names, commit
messages. Assume it can be steered. The design question is what a fully steered model can
actually do, and the answer is bounded by three things it cannot reach past.

**It cannot leave the catalog.** A `ContractRef` naming anything the catalog doesn't list is
`ErrOutsideCatalog` (`internal/contract/contract.go:20`), refused at proposal time and again
at execution time. Prompt-level instructions do not participate in that check.

**It cannot pick its own magnitude.** Scope parameters are authored with `min`/`max`/`default`
in the catalog. The model proposes a catalogued action; the bounds come from the file.

**It cannot grant itself permission.** A candidate carries a *requested* governance band.
Converting a request into allow/hold/deny happens in hiss, in a separate process, under a
separate identity that is the only one the broker permits to publish on `thump.decisions`.

Raw payloads never enter the conversation at all: an `EvidenceRef` carries a digest, a query
and a backend ref, and has no `Raw` field — a boundary stated in `api/v1/proposal`'s own doc
comment as one that will never be added.

**The residual, precisely.** Identifiers are not masked. A namespace, a pod name, a
service name and a metric name all reach the provider as written. An obfuscation
layer was removed rather than repaired: it hid the affected service's name in the
prompt while the tool descriptions named every evidence query, label key and
workload on the rig — so it bought no confidentiality anyone could state, and cost
the reasoner the one key it needed to join three tools (D-24). What bounds a steered
model is the three clauses above, none of which depend on the provider not knowing
what things are called.

## The trust ceiling

No autonomous write authority until four mechanisms are simultaneously operational: runtime
governance, action contracts with automatic reversal, signal contracts with declared
guarantees, and calibrated confidence. Three of four is not enough, and prompt-level safety
counts as none of them. All four have been operational since 2026-07-16, when a real unmet
success window fired a real reversal on the rig. The rule stands as the bar any future
increase in write authority has to clear again ([I-12](invariants.md#i-12--the-trust-ceiling)).

## The operator surface never disposes

A human interface onto this engine may **read** emitted state, or **emit an ack**. It may
not write verdicts, execute an action, or arm the kill switch. A held action resumes because
hiss re-issues an approved decision once the ack satisfies the condition hiss itself
attached — so governance still happens exactly once, in hiss.

One exception is declared: `calipers force`, a human disposing in hiss's place. It is
attributed, audited, rendered `forced` in every view, and still refused by a disarmed kill
switch. Force overrides governance; it never overrides the physical stop.

The `ApprovalRequest` custom resource is deliberately not a second force path. `spec.decision`
accepts `approve` and nothing else, because break-glass five characters away from the normal
path on the same object under the same RBAC verb is not break-glass. A resource is born
undecided, and a controller that cannot attribute an ack refuses to publish it — a cluster
missing its admission policies fails closed.

## Not defended

Stated because a threat model that only lists wins is marketing.

- **Same-node pod-to-pod traffic on the Prometheus and Loki legs is in the clear.** Both
  backends are vendored charts that don't terminate TLS; the client side is built and the
  flip is a values change. Cilium WireGuard covers node-to-node, which is not the same thing.
- **Active WAL segments are plaintext** on `emptyDir`, mode `0o600`. Nothing outlives the
  pod, and this is the copy `calipers` reads during an incident.
- **NATS at-rest encryption is chart-wired and unproven.** The restart-and-`strings`-over-`/data`
  check against a live rig is still owed.
- **The catalog cannot describe your cluster.** An action that pauses a reconciler doesn't
  hold if something else reconciles it back, and the contract has no way to warn you. This
  bit us on our own rig; the account is D-10.
- **`d.Approver` from the CLI path is self-asserted.** It's whatever string a human typed
  into `calipers approve --approver`. The `ApprovalRequest` path exists precisely because
  that field is unverifiable, and it is the only path where the approver is an authenticated
  subject.
