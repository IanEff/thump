# thump

[![CI](https://github.com/IanEff/thump/actions/workflows/ci.yml/badge.svg)](https://github.com/IanEff/thump/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/IanEff/thump?logo=go)](https://go.dev)
[![Go Reference](https://pkg.go.dev/badge/github.com/IanEff/thump.svg)](https://pkg.go.dev/github.com/IanEff/thump)
[![License](https://img.shields.io/github/license/IanEff/thump)](LICENSE)

![Thump](assets/rainbow_thump.png)

A bounded SRE loop for Kubernetes with a reasoning plane bolted into the middle. It watches reliability signals, investigates an evidence snapshot with an LLM, and executes an authored, catalog-bound action once governance clears.

What it can act on is determined by the action catalog. In the shipped profiles at `config/<profile>/actions/catalog.yaml`, that catalog holds seven actions: three Rook/Ceph runbooks (`hold-rebalance`, `accelerate-recovery`, `throttle-non-critical-paths`), three against the OpenTelemetry Astronomy Shop demo (`disable-product-catalog-failure`, `disable-cart-failure`, `restart-cart-pod`), and one synthetic `acme` domain action (`acme-shed-load`). Adding remediation capabilities means adding catalog entries; the reasoner and the governor stay untouched. [Onboarding a domain in config alone](#onboard-your-own-domain) exercises that boundary.

The safety argument rests on deliberate rigidity. The reasoner cannot invent an action outside the catalog, fabricate parameter magnitudes, bypass the readiness gate on a hunch, or execute anything governance has not approved. A fixed catalog bounds blast radius, governance evaluates policy without re-reasoning, the kill switch fails closed, an undo fires on success as well as failure, and the engine declines explicitly when evidence falls short.

## Try it, without a cluster

```sh
git clone https://github.com/IanEff/thump && cd thump
task ci
```

Three and a half minutes on a cold clone with an empty build cache, requiring neither Kubernetes nor an `ANTHROPIC_API_KEY`. The pipeline executes `gofmt` checks, `go vet`, `golangci-lint`, `govulncheck`, five strict `kubeconform` schema passes over the Helm chart and custom resources, `promtool` unit tests against SLO recording rules, the test suite under `-race`, integration tests, and compiles six binaries.

The test that exercises the full composition:

```sh
go test ./test/onboarding -v
```

That drives all five beats across a domain authored in seven YAML files. The fixture domain is called `acme` so that any accidental requirement for a domain-specific Go discriminator immediately breaks the test.

The full command list is under [Building & testing](#building--testing). Setting up a local cluster is covered [further down](#standing-it-up-locally).

## Watch it run

A real incident on the `thump-test` rig (GCE VMs running k3s): a fault is injected, `rattle` fingerprints the SLO burn, `clank` gathers evidence and proposes a catalogued action, `hiss` rules on it, and `thump` executes then watches for convergence. The rig is public — [github.com/IanEff/thump-test](https://github.com/IanEff/thump-test) — driven by scenarios in [`chaos/scenarios.yaml`](chaos/scenarios.yaml).

Two load-bearing mechanics govern actuation. The reversal operation is bound from the catalog before execution begins, guaranteeing an undo path exists. A missed success window fires that undo automatically. On 2026-07-26 that path ran against Ceph, adjusted two OSD tunables mid-incident, settled `success`, and restored default configuration.

[The same loop is worked step by step](#a-golden-path-worked-end-to-end) against an uncompiled service.

## Provenance, stated up front

The four-plane architecture, boundary-object discipline, incident-response loop, and belief-formation defenses are David Jambor's, from *Agentic Reliability Engineering* (O'Reilly). thump exists because the book left its two central mechanisms unspecified: calibrated confidence and the learning loop were treated as tuning details rather than architectural foundations. Building those two mechanisms is what this repository actually did.

Build methodology comes from John Arundel's *The Power of Go: Tools* and *The Power of Go: Tests* (Bitfield). Delivery and layout conventions come from Joel Holmes's *Shipping Go* (Manning). Where a rule traces to one of them, [`docs/invariants.md`](docs/invariants.md) documents the constraint; where the implementation departs from Jambor, [`docs/design-decisions.md`](docs/design-decisions.md) records the rationale.

Evaluating this engine for your own systems starts in [Authority model & guardrails](#authority-model--guardrails) and [Onboard your own domain](#onboard-your-own-domain). Mechanism-level specifications live in [`docs/`](#documentation).

---

## Table of contents

- [Try it, without a cluster](#try-it-without-a-cluster)
- [Watch it run](#watch-it-run)
- [Why this shape](#why-this-shape)
- [Authority model & guardrails](#authority-model--guardrails)
- [The five beats](#the-five-beats)
- [Repo tour](#repo-tour)
- [A golden path, worked end to end](#a-golden-path-worked-end-to-end)
- [Onboard your own domain](#onboard-your-own-domain)
- [Install](#install)
- [Standing it up locally](#standing-it-up-locally)
- [Invariants (read as law)](#invariants-read-as-law)
- [Known-open](#known-open)
- [Building & testing](#building--testing)
- [Contributing](#contributing)
- [Documentation](#documentation)

---

## Why this shape

Connecting alert feeds directly to an unconstrained LLM shell creates three failure modes simultaneously.

Observation collapses into interpretation. When an alert arrives stating "system degraded", the model reasons over an upstream conclusion without inspecting the underlying telemetry.

Blast radius remains unbounded. An action space defined by shell access makes containment depend on model judgment, which cannot be audited ahead of time.

Policy becomes invisible. Rules such as freeze windows turn into conditional branches inside prompts or reasoner loops, where they cannot be audited, tested, or tightened independently.

thump enforces structural separation at every boundary. Detection cannot interpret facts. Reasoning cannot mutate infrastructure. Governance evaluates policy without re-reasoning. Execution applies approved contracts without deciding policy.

---

## Authority model & guardrails

Remediation magnitudes are declared in catalog configuration (`config/<profile>/actions/catalog.yaml`). In the shipped catalog, `throttle-non-critical-paths` specifies a `severityReductionPct` of 0.7, `accelerate-recovery` specifies 0.8, and `restart-cart-pod` specifies 0.1. Restarting the cart pod is a valid, low-blast operation that passes readiness, but the 0.1 forecast separates it from `disable-cart-failure` (0.9) because the fault is flagd flag state rather than container state. The LLM selects which action fits the diagnosis; it cannot declare that a throttle will reduce severity by 73%. An unforecasted action carries `nil`, which renders as `unmeasured` so absence stays distinct from zero effect.

Hypothesis confidence is computed deterministically. The output score is the product of rattle's signal-strength confidence, a grounding multiplier based on live citations across distinct telemetry backends, a case-base alignment term (active once the case base reaches its two-vote floor), and causal likelihood scores from change events. The model's self-reported confidence acts strictly as a ceiling via `min()`. A high self-score without corroborating evidence is capped; it cannot inflate itself.

Blast tiers, a kill switch, and deduplication windows bound runtime risk. Every action carries an authored `blastTier` (`low`/`med`/`high`). `accelerate-recovery` is marked `high` because trading client I/O for recovery concurrency requires explicit human approval. hiss evaluates tier and reversibility against `config/<profile>/hiss/policy.yaml`, placing actions exceeding the auto-fire ceiling into a `hold` state.

Underneath governance sits a file-based kill switch (`THUMP_KILLSWITCH`, `internal/thump/killswitch.go`) that fails closed on missing, unreadable, or invalid files. A reload failure clears armed state immediately. Disarmed executions record `{mode: live, result: blocked}` to ensure visibility. A `DedupeWindow` (default 1h, configured via `DEDUPE_WINDOW`) prevents recurring alert firings from launching overlapping actions for the same fingerprint.

Reversal operations are exempt from the kill switch. Halting an active remediation mid-flight risks stranding infrastructure in a partial state, so an approved undo is allowed to complete.

Reversals trigger on two distinct conditions. A missed convergence window initiates an undo. Additionally, an action declaring `restoreOnSuccess: true` (such as `accelerate-recovery` or `hold-rebalance`) reverts its temporary tunables once convergence succeeds, settling the outcome as `success`. This prevents temporary tuning adjustments from persisting indefinitely.

Declining to act is a first-class result. Emitting `no_action` with a cited reason (`ProposalSet.Status.Reason`) is an expected execution path. It triggers when evidence is insufficient, when a proposed action does not match the diagnosed failure class (`errClassMismatch`), when cited telemetry was never collected, or when the readiness gate vetoes a candidate.

---

## The five beats

```
SignalDetection        ProposalSet             Decision                Outcome
      │                     │                       │                      │
  ┌───▼───┐   detects   ┌───▼───┐   selects     ┌───▼───┐   permits    ┌───▼───┐
  │rattle │────────────▶│ clank │──────────────▶│ hiss  │─────────────▶│ thump │
  └───────┘             └───────┘               └───────┘              └───┬───┘
   Signal                Reasoning              Governance                 │ acts
                                                                           ▼
                                                                    (the cluster)
                                                                           │
                                                     click reads Outcome back into
                                                     clank's case base — the return
                                                     edge, not a sixth box
```

| Beat | Plane | Job | Never |
|---|---|---|---|
| `rattle` | Signal | Detects reliability divergences, emits a fingerprinted `SignalDetection` | Never interprets — facts only |
| `clank` | Reasoning | Assembles an evidence snapshot (the SAO), investigates with read-only tools, proposes a ranked, confidence-scored `ProposalSet` | Never acts — proposals only |
| `hiss` | Governance | Evaluates a `ProposalSet` against policy (confidence floors, blast-tier ceilings, freeze windows), emits one `Decision` | Never re-reasons — verdicts only |
| `thump` | Execution | Renders (dry-run) or executes (live) an approved `Decision`, watches for convergence, undoes on a missed window or an authored restore | Never decides — contracts only |
| `click` | Learning | Feeds `Outcome` records back into clank's case base and calibration | Never a module — wiring, not a binary |

clank observes three strict boundaries: it trusts rattle's signal and fingerprint without re-evaluating divergence, its entire output is a document, and its candidate authority levels are requests that only hiss can convert into permits.

These boundaries are formalized in [`CHARTER.md`](CHARTER.md). Detailed component interactions are documented in [`docs/architecture.md`](docs/architecture.md).

---

## Repo tour

Implementation entry points and packages for each beat:

| Beat | Entrypoint | Package | Open first |
|---|---|---|---|
| rattle | `cmd/rattle` | `internal/rattle` | `doc.go` |
| clank | `cmd/clank` | `internal/clank` | `doc.go` |
| hiss | `cmd/hiss` | `internal/hiss` | `hiss.go` |
| thump | `cmd/thump` | `internal/thump` | `thump.go` |
| click | none (wiring) | `internal/clank/click.go`, `metrics.go` | `Click.Absorb` and `ReturnEdge` in `click.go` |

Supporting commands in `cmd/` include `bootstrap` (in-cluster setup Job) and `calipers`, the operator CLI. The twelve calipers subcommands — `incidents`, `approve`, `force`, `unseal`, `corpus`, `rca`, `tune`, `replay`, `harvest`, `probe`, `transcript`, `scorecard` — are dispatched by verb in `internal/calipers/calipers.go`. Investigation tools (`metrics`, `loki`, `kube`) live in `internal/evidence`, LLM provider clients in `internal/anthropic` and `internal/gemini`, and coordinate resolution in `internal/subjects`.

`internal/clank/doc.go` is the primary reference for reasoning loop invariants and belief-formation defenses ([`docs/invariants.md`](docs/invariants.md), I-6). rattle and hiss handle signal detection and policy evaluation; clank implements bounded LLM investigation.

Test utilities sit alongside their respective packages: `natstest` (embedded NATS server), `s3test`, `configtest`, `leaftest` (dependency leaf validation), and `tlsxtest`.

`internal/tlsx` and `internal/tlsxtest` centralize TLS configuration. `tlsx.go` is the sole location where `*tls.Config` instances are constructed. `tlsxtest.go` generates ephemeral ECDSA certificate authorities per test, enabling deterministic testing of negative handshake paths (`TestClient_ServerLeafFromDifferentCA_HandshakeRefused`, `TestClient_ExpiredServerLeaf_HandshakeRefused`, `TestServer_ClientPresentsNoCertificate_HandshakeRefused`, `TestServer_RotatedKeypair_PickedUpWithoutRestart`) without expiring static fixtures.

---

## A golden path, worked end to end

The engine operates over arbitrary domains configured via YAML. The walkthrough below traces `TestOperator_OnboardsANewDomainInConfigAlone` from `test/onboarding/onboard_test.go`, which runs in `task ci` without credentials or external infrastructure:

1. **rattle detects.** `acme_api_error_ratio` diverges from baseline (observed 0.42 against a 0.001 baseline, `severity.DegradationPct: 0.42`, trajectory `accelerating`). rattle assigns fingerprint `fp-acme-api-availability-001` and emits a `SignalDetection` at 0.9 signal confidence.
2. **clank reasons.** Intake builds the Situational Awareness Object (SAO) with topology from `whir/catalog-info.yaml` and state queries (`acme-db` and `acme-cache` both returning `healthy`). clank executes the `metrics` tool for `acme_api_error_ratio` and the `loki` tool for `namespace: acme`, citing both (`acme_api_error_ratio` and `loki-1`). It proposes `acme-shed-load` (`blastTier: low`, reversal `restore-acme-capacity`, scope parameter `serving_replicas: 2`).
3. **The gate evaluates.** `budgetOK`, `dedupeOK`, and `evidenceOK` all evaluate to true. Two live citations across distinct backends (Prometheus and Loki) satisfy the grounding requirement, producing a computed confidence of 0.9.
4. **hiss governs.** The `tier-1` confidence floor for `service_failure` is 0.75; 0.9 clears it. The low blast tier maps to `RiskBand: act_reversible`, which falls within the auto-fire threshold. The decision records `Verdict: approved`, `policyVersion: acme-v1`, and `floorApplied: 0.75`.
5. **thump acts.** In default dry-run mode, the order is rendered: `Outcome{mode: dry_run, result: rendered}`. In live mode, thump scales `acme-api` to 2 replicas, monitors `acme_api_error_ratio` over the 5-minute window, and executes `restore-acme-capacity` if convergence fails.

Declines follow the same audit trail. In `internal/clank/testdata/detections/ceph-rgw-saturation.yaml`, captured JetStream telemetry shows clank identifying a `traffic_shift` class for which no catalog action is registered. clank proposes nothing, hiss classifies the input as `ReasonUngatedInput`, and thump declines execution.

Every step maintains provenance by threading `signalRef` through the pipeline: `Detection.Fingerprint` → `ProposalSet.SignalRef` → `Decision.SignalRef` → `Outcome.SignalRef`.

---

## Onboard your own domain

Onboarding requires no Go modifications. Signals, evidence queries, topology, failure classes, action catalogs, and execution mechanics are authored entirely in YAML. The layout in `test/onboarding/testdata/acme/` provides the reference template:

| File | Responsibility |
|---|---|
| `rattle/watch.yaml` | Polled SLO metrics and dependency health gates |
| `whir/catalog-info.yaml` | Backstage-format dependency graph |
| `whir/state-queries.yaml` | PromQL queries verifying dependency health |
| `whir/evidence-queries.yaml` | Read-only PromQL queries available to clank |
| `actions/failure-classes.yaml` | Domain failure class definitions |
| `actions/catalog.yaml` | Remediation actions, blast tiers, and execution steps |
| `hiss/policy.yaml` | Confidence floors, blast ceilings, and freeze windows |

An action's `execution` block maps to compiled mechanisms in `internal/actuate`:

```yaml
- name: acme-shed-load
  applicableFailureClasses: [service_failure]
  applicableTiers: [tier-1]
  blastTier: low
  reversal: {method: restore-acme-capacity, fallback: page-oncall}
  execution:
    forward: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 2}]
    reverse: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 10}]
```

The compiled verb set — `scale`, `restart`, `flagVariant`, `exec`, and `maintenanceRelease` — is validated at process startup. An unmapped verb, missing target parameter, or forward step lacking a reversal fails validation immediately.

> ⚠️ **A catalog PR is an execution-surface PR.** The `exec` verb accepts command arguments directly, allowing anyone who can merge `config/<profile>/actions/catalog.yaml` to execute commands within pods accessible to thump's ServiceAccount. Access is bounded by Kubernetes RBAC (`pods/exec` per namespace), the kill switch, and hiss policy. ServiceAccounts must be scoped tightly; see `CONTRIBUTING.md`.

Configuration details and environment variables are documented in [`docs/onboarding.md`](docs/onboarding.md).

---

## Install

Release archives are published for Linux and macOS (amd64 and arm64), bundling the four services (`rattle`, `clank`, `hiss`, `thump`) and `calipers`. `bootstrap` is an in-cluster Job that ships exclusively as a container image:

```sh
gh release download v0.1.0 --repo IanEff/thump --pattern '*_linux_x86_64.tar.gz'
tar xzf thump_0.1.0_linux_x86_64.tar.gz
```

Multi-arch container images for `rattle`, `clank`, `hiss`, `thump`, and `bootstrap` are published with attached SBOMs and SLSA provenance attestations, signed keylessly via Sigstore/cosign. `calipers` is distributed as a binary only. Building from source via `task build` outputs all six binaries to `bin/`.

---

## Standing it up locally

The codebase supports two cluster profiles defined in `Tiltfile`: `dev` and `thump-test`.

`dev` provisions a self-contained local k3d cluster with Cilium, cert-manager, Prometheus, Alertmanager, Loki, Tempo, S3Mock, the OpenTelemetry demo, and the synthetic `acme` domain. Detailed instructions are in [`docs/dev-environment.md`](docs/dev-environment.md).

`thump-test` runs against GCE VMs hosting k3s with Rook/Ceph and the OpenTelemetry Astronomy Shop demo.

```sh
task dev:up                        # local k3d dev cluster
tilt up -- --cluster=thump-test    # remote thump-test rig
```

Dry-run execution is active by default (`THUMP_EXECUTOR=dry`). Live execution requires setting `THUMP_EXECUTOR=live` alongside an armed `THUMP_KILLSWITCH` file. When the kill switch is disarmed, live attempts emit `ResultBlocked`.

The operator surface provides inspection and approval workflows:

```sh
task dev:certs                      # extract NATS client certificates
task dev:incidents                  # list active and held incidents
task dev:approve FP=<fingerprint>   # approve a held action
```

Held actions can also be released via Kubernetes custom resources (`approvalRequests.enabled: true` in `deploy/chart/thump/values.yaml`). When enabled, hiss creates an `ApprovalRequest` custom resource that an authorized user approves via standard RBAC:

```sh
kubectl -n thump get approvalrequests
kubectl -n thump patch approvalrequest ar-8f3c1d2e0a5b7649 \
  --type=merge -p '{"spec":{"decision":"approve"}}'
```

Under custom resource approval, a `MutatingAdmissionPolicy` validates the user identity and records the approver into `thump.dev/approved-by`, producing an independent Kubernetes audit log entry. The controller accepts only `spec.decision: approve`. Bypassing governance is reserved for `calipers force` (D-9).

Environment configuration variables are defined in `internal/config/config.go`.

---

## Invariants (read as law)

Architectural rules are numbered for reference in code reviews and design discussions. Full specifications and test mappings are in [`docs/invariants.md`](docs/invariants.md).

1. **Signals describe state, never interpretation.** "p99 412ms vs 38ms baseline" is a signal; "system degraded" is an interpretation. rattle emits raw measurements.
2. **Two confidence numbers, never one field.** Signal-strength confidence belongs to rattle (`signal.Divergence.Confidence`). Hypothesis confidence belongs to clank (`proposal.Candidate.Confidence`), computed from evidence corroboration and historical alignment.
3. **Policy lives only in Governance.** Criticality tiers, error budgets, and confidence thresholds belong exclusively in hiss. clank and rattle contain zero policy rules.
4. **The catalog is the autonomy boundary.** Blast radius is bounded by declared action contracts, not model judgment. Actions outside the catalog produce immediate errors.
5. **Gate ≠ shaper.** The readiness gate evaluates a conjunction of minimums (`budget ∧ dedup ∧ evidence`). Risk shaping evaluates execution autonomy separately.
6. **The five belief-formation defenses are not optional.** Includes a >=2-source corroboration floor, freshness decay on historical alignment, decrements for predicted-but-absent indicators, representable `partial_non_converging` outcomes, and mandatory live telemetry citations.
7. **Reasoning selects, Governance permits.** clank requests an authority level; hiss grants or denies permission without re-ranking or modifying proposals.
8. **Learn is a return edge, not a module.** click is the return path delivering `Outcome` records to clank's case base; it is not a standalone service.
9. **The signal contract owns the `if`.** Freshness constraints and significance thresholds reside in rattle's contract. Degraded trust attenuates confidence without dropping the signal.
10. **Nothing executes ungoverned.** Execution defaults to dry-run, requires an armed kill switch, and binds an executable reversal path.
11. **The log is the system of record.** Detections, proposals, decisions, and outcomes are persisted to an S3 write-ahead log. etcd stores only human-authored configuration.
12. **The Trust Ceiling.** Autonomous write access requires four operational pillars: runtime governance, automatic reversals, declared signal contracts, and calibrated confidence.
13. **Every wave stays red→green.** Unverified seams do not cross into subsequent components.
14. **Delivery is at-least-once; identity is the fingerprint.** Consumers are idempotent and deduplicate using producer-assigned fingerprints rather than transport metadata.
15. **The operator surface never disposes.** User interfaces read state and emit approval events; they do not write decisions. The sole exception is break-glass `calipers force`.
16. **Every leg is TLS-negotiated by this process, or declared plaintext.** TLS configs are constructed exclusively in `internal/tlsx`. `InsecureSkipVerify` is disallowed. Plaintext exceptions are explicitly cataloged.
17. **Every client this engine dials has an authored retry policy, timeout, and a failure that surfaces.** Network clients require explicit timeouts and error propagation.

---

## Known-open

- **Operator tooling is CLI-focused.** `calipers incidents`, `calipers approve`, and `calipers force` provide core hold and approval workflows, but drill-down telemetry views and web interfaces are not built.
- **Topology-aware prior adjustment is planned.** Actions carry static baseline `severityReductionPct` values; dynamic adjustment based on situational awareness object topology is not yet implemented.
- **Risk shaping uses a two-factor lattice.** `RiskBand` is derived from reversibility and blast tier. A continuous composite risk score is deferred ([`docs/design-decisions.md`](docs/design-decisions.md), D-3).
- **`accelerate-recovery` requires reconciler coordination.** Pausing `rook-ceph-operator` during recovery requires GitOps tools (e.g., ArgoCD) to ignore replica mutations on that deployment ([`docs/design-decisions.md`](docs/design-decisions.md), D-10).
- **Chaos-mesh v2.8.3 IOChaos limitation.** `toda` injector panics when targeting Ceph OSD FUSE layers, preventing end-to-end chaos testing for that specific fault class.
- **ServiceMonitor configuration.** Missing scrape configurations can obscure operational metrics; verify Prometheus scrape targets when diagnosing pipeline status.

---

## Building & testing

Build automation uses [go-task](https://taskfile.dev) (`Taskfile.yaml`). Run `task --list-all` for the full target catalog.

| Command | Description |
|---|---|
| `task run:clank` / `run:rattle` / `run:hiss` / `run:thump` / `run:calipers` | Run an individual beat or the operator CLI |
| `task build` | Build all six binaries into `bin/` |
| `task ci` | Local CI: fmt-check → vet → lint → vulncheck → chart-lint → promql → race → integration → build |
| `task test` / `task race` | Run unit test suite (with `-race`) |
| `task coverage` | Generate test coverage profile |
| `task vulncheck` | Run govulncheck over dependencies |
| `task chaos:preflight` | Verify preconditions and disable reconciler self-healing before chaos runs |
| `task eval` | Run reasoner evaluation against production catalog (requires API key) |
| `task rca` | Run graded RCA benchmark suite |
| `task corpus` | Mine sealed WAL into `testdata/corpus/` |
| `go test ./test/onboarding -v` | Execute five-beat domain onboarding test offline |
| `go test ./internal/clank -run TestGate -v` | Run a specific test |

`task ci` passing is the requirement for completion. Pull requests must pass local `task ci` before submission.

---

## Contributing

Reviews focus primarily on invariant preservation: verifying that policy does not leak into the reasoner, raw payloads remain outside conversation contexts, fingerprints are preserved without recomputation, and vocabulary constraints are respected.

`CONTRIBUTING.md` provides detailed workflow guidelines. Go conventions, comment formatting, and testing standards are defined in `AGENTS.md`. Security vulnerabilities must be submitted via `SECURITY.md`.

---

## Documentation

| Document | Content |
|---|---|
| [`CHARTER.md`](CHARTER.md) | Architectural contract: core refusals, smell tests, and non-goals |
| [`docs/architecture.md`](docs/architecture.md) | Component architecture, plane separation, boundary objects, and data flow |
| [`docs/invariants.md`](docs/invariants.md) | Seventeen invariants with sourcing, violation examples, and test assertions |
| [`docs/onboarding.md`](docs/onboarding.md) | Guide for onboarding new domains in YAML |
| [`docs/dev-environment.md`](docs/dev-environment.md) | Setup instructions for the local k3d dev environment |
| [`docs/threat-model.md`](docs/threat-model.md) | Security model, actor capabilities, and boundary enforcement |
| [`docs/design-decisions.md`](docs/design-decisions.md) | Decision log recording divergences from literature |
| [`docs/c4-architecture.md`](docs/c4-architecture.md) | C4 architecture diagrams and interaction sequences |
| `AGENTS.md` | Go coding standards, doc comment voice, and testing rules |
| `CONTRIBUTING.md` | Development workflow and code review standards |

### Provenance citations

- **David Jambor, *Agentic Reliability Engineering*** (O'Reilly) — Four-plane architecture, boundary objects, incident-response lifecycle, and belief-formation defenses. Sourced vocabulary: `SignalDetection`, `ProposalSet`, `ActionContract`, Situational Awareness Object, and Trust Ceiling. Divergence rationale is documented in [`docs/design-decisions.md`](docs/design-decisions.md).
- **John Arundel, *The Power of Go: Tools*** and ***The Power of Go: Tests*** (Bitfield) — Build architecture, functional core/imperative shell separation in actuation, and test naming standards.
- **Joel Holmes, *Shipping Go*** (Manning) — Release pipeline, binary packaging, and repository layout.
