# The thump charter

What this engine promises, what it refuses, and how to tell when a change has broken one of
those. It changes slowly on purpose. Status lives in `CHANGELOG.md`; mechanism lives in
[`docs/architecture.md`](docs/architecture.md); the rules and their tests live in
[`docs/invariants.md`](docs/invariants.md). This file is the contract those three serve.

## Five beats, and what each one is forbidden to do

Each beat owns one job and one refusal. The refusals are the architecture — delete them and
what's left is an ordinary automation pipeline with a language model in it.

| Beat | Plane | Job | Never |
|---|---|---|---|
| `rattle` | Signal | Detects reliability divergences, emits a fingerprinted `signal.Detection` | **Never interprets** — facts only |
| `clank` | Reasoning | Assembles an evidence snapshot, investigates with read-only tools, emits a ranked `proposal.Set` | **Never acts** — proposals only |
| `hiss` | Governance | Evaluates a `proposal.Set` against policy, emits one `decision.Decision` | **Never re-reasons** — verdicts only |
| `thump` | Execution | Renders or executes an approved decision, watches convergence, undoes on a missed window | **Never decides** — contracts only |
| `click` | Learning | Feeds `outcome.Outcome` back into clank's case base and calibration | **Never a module** — wiring, not a binary |

Planes are conceptual separations; they need not be separate processes. Today it's one binary
per beat in one Go module, and that's fine **as long as the four responsibilities stay
structurally distinguishable**. The moment they mash together the safety properties stop
holding, and they stop holding quietly. That's why each refusal has its own numbered
invariant and its own test, instead of living in a style guide.

The objects that cross between them — who produces each one, who may only read it — are in
[`docs/architecture.md`](docs/architecture.md#the-seams-producer-owned-boundary-objects).

## The smell test

Run this at a design review, and any time a new field doesn't obviously belong to one
boundary object. Four questions; a "no" is a finding.

1. Can every piece of state be pointed to **exactly one** boundary object?
2. Does anything bound blast radius **other than** a declared action contract's scope?
3. Does the reasoner read anything that **isn't** a contract-bound signal?
4. Is there any policy check living **anywhere but** governance?

## What this will never be

Distinct from [what isn't built
yet](docs/architecture.md#what-is-deliberately-not-built) — those are deferred. These are
refusals, and reversing one is a charter change.

| Refusal | Why it stands | Enforced at |
|---|---|---|
| click never becomes a service | A "Learn service" has to read signals, proposals, verdicts *and* outcomes, quietly reassembling the monolith the planes just decomposed | `internal/clank/click.go` — it's wiring |
| The model never invents an action | It selects from an authored catalog, or it is refused. It also cannot invent a magnitude the action's author didn't authorize, nor convert its own request into permission — that verb is hiss's alone | `ErrOutsideCatalog`, `internal/contract/contract.go:20` |
| `InsecureSkipVerify` is never a declared exception | A plaintext leg can be authored with its reason on record. An unauthenticated TLS session dressed as a secure one cannot | `internal/httpx/tripwire_test.go:205`, categorical, no allowlist |
| The operator CLI never ships as a container image | Cluster-mutating startup work belongs to a one-shot Job (`cmd/bootstrap`), so the operator surface doesn't need to run in-cluster to be useful | `Taskfile.yaml`'s `CALIPERS`, held out of `IMAGE_BEATS` |
| Confidence weights are authored, never fitted to a corpus this small | The re-entry criterion is written down so it stops being re-litigated each time someone notices the numbers are hand-set. Today the case base holds one settled incident | `docs/design-decisions.md` D-20 |
| Coverage is never the goal | One lab, two unrelated domains, chosen so a fault in one can never be matched by an action authored for the other | `chaos/scenarios.yaml` — the two rows share no failure class |

**Not a CRD control plane** is the one that needs more than a row. Detections, proposals,
decisions and outcomes ride the stream and land in a sealed write-ahead log; etcd holds slow
human-authored config and as little of that as possible. A custom resource is *earned* by a
demonstrated reconciliation or status need, never granted by default. That bar has been
cleared exactly once, for `ApprovalRequest`
(`deploy/chart/thump/crds/approvalrequest.yaml`), and only on an argument no amount of log
replay can satisfy: under a custom resource the authority to approve is Kubernetes RBAC and
the witness is the API server's own audit record, where every other audit claim this engine
makes is the engine attesting about itself. `ActionContract` and hiss's `Policy` stay named
candidates, unbuilt.

## The limit we can't fix from in here

An action contract describes what to do. It cannot describe *your* cluster, and the gap has
already drawn blood: `accelerate-recovery` pauses the Rook operator so its settings hold,
and on our own rig ArgoCD reverted that in about a second. It works now only because the rig
was changed to stop fighting it — a property of someone's deployment, not of the contract.
Run your own reconciler and you get that bug back, and nothing in the catalog can warn you.
The full account is `docs/design-decisions.md` D-10.

That's the honest edge of the whole design, and it's why a human who knows the cluster
authors the catalog.
