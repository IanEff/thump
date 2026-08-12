# Changelog

All notable changes to thump are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[SemVer](https://semver.org/).

This file is hand-written from the phase history rather than generated from
`git log` — it describes what the engine can *do*, not which commits touched
it. GitHub also generates a commit-based release changelog per tag (see
`.goreleaser.yml`'s `changelog:` block); that's a different artifact and
doesn't replace this one.

## [Unreleased]

### Added

- **Phase AH: Forge package & GitHub client integration.**
  - Introduced `internal/forge` with a `GitHub` client supporting repository release creation and pull request workflows via Personal Access Token (PAT) authentication.
  - Wired forge release creation (`maintenanceRelease` verb) into `thump`'s live execution path, passing real `proposal.Set` context rendered via `Order.Notes`.

### Changed

- **The public docs are rewritten for a reader with no internal context.**
  `docs/architecture.md`, `docs/invariants.md`, and `docs/onboarding.md` were
  provisional ports from private design notes, banners and all; they're now
  written for outside developers and users, and the banners are gone.
  `docs/invariants.md` regains the per-rule sourcing the port dropped, plus, for
  each rule, the test that would go red.
- **New: `docs/design-decisions.md`** — the public record of where this project
  knowingly departs from the book it's built from, what it does instead, and
  why, including the entries that are parked and the ones that were declined
  with reasons. The dated working journal stays private; everything
  load-bearing from it is here.
- `README.md` reorganized around the question an outside reader actually
  arrives with — *what stops this from doing something stupid to my cluster* —
  and corrected where it had drifted: the operator surface is built, not just
  designed; the kill switch's reversal exemption is stated rather than glossed;
  the undo's success-restore trigger is described.
- **The operator CLI is `calipers`, renamed from `trim`.** It grew from two
  verbs to nine — `incidents`, `approve`, `force`, `unseal`, `corpus`, `rca`,
  `tune`, `replay`, `harvest` — of which only `approve` and `force` publish
  anything. Anywhere the 0.1.0 notes below say `trim`, read `calipers`.
- Removed `docs/phase-k-live-rig-test-plan.md`,
  `docs/aa1-harvest-continuation.md`, and `docs/superpowers/` — internal working
  documents that had leaked into the public tree.

## [0.1.0] — first tagged build

The first release a stranger can download and run without a Go toolchain.
Not feature-complete — see "Known gaps" below — this tag exists so the
project has a welcoming build, not because the engine is finished.

### Added

**The five-beat pipeline** — rattle (detect) → clank (reason) → hiss
(govern) → thump (act) → click (learn) — all live, all wired end to end
through the NATS JetStream broker, with Prometheus metrics and OTel tracing
at each beat's seams.

- **rattle** — signal detection: acceleration/burn-window detectors,
  historical-envelope baselines, topology-aware enrichment, correlation
  across signals, a per-site watch list authored in config.
- **clank** — bounded LLM reasoning: proposes only from the compiled action
  catalog, never off-catalog; five belief-formation defences (≥2-source
  floor, freshness decay, negative-signal check, partial-convergence
  tracking, forced-live-citation); evidence enters as digests only, never
  raw payloads; WAL-backed transcript persistence for audit; model
  adapters for the Anthropic and Gemini backends with backoff/retry.
- **hiss** — governance: authority and confidence validation, blast-tier
  and risk-band authoring, an append-only decision ledger; the gate is a
  conjunction of minimums (budget ∧ dedup ∧ evidence) — one failing
  dimension vetoes the whole `ProposalSet`, not just the top choice.
- **thump** — action: a live executor gated behind `THUMP_EXECUTOR` and a
  file-based kill switch; the `actuate` package execs into pods,
  merge-patches resources, and scales deployments, entirely from
  catalog-authored steps; multi-step forward sequences (`seqOp`) run in
  authored order and stop at the first failing step.
- **click** — learning: a case base absorbs outcomes and exposes a
  prior-read path so history can inform future proposals.

**Automatic reversal, including the success case.** A governed action's
undo now has two triggers, not one: a failure still reverses, and an
action whose catalog contract declares `restoreOnSuccess: true` (a
temporary tuning knob, not a fix) restores its authored defaults on a
*met* window too — closing the case where a win left a change applied
indefinitely with nothing watching it.

**Config-only onboarding, proven end to end.** A new domain — signals,
evidence queries, topology, failure classes, the action catalog, and its
execution binding — onboards entirely as operator-authored YAML, no Go
required. `test/onboarding` drives a synthetic domain through all five
beats hermetically, with no live cluster and no API key, as the proof.

**Operational hardening.** The four long-running beats' container images
are built multi-arch (linux/amd64, linux/arm64) with SBOM and provenance
attestation and signed keylessly via Sigstore/cosign, each running with a
hardened security context and liveness/readiness endpoints; Cilium
network policies scope egress per container. `trim` ships as a plain
downloadable binary instead — it's an operator-run CLI, not a cluster
service. Renovate keeps `go.mod`, `Dockerfile`, and GitHub Actions
dependencies current.

**A gated CI chain** (`task ci`): fmt-check → vet → lint → vulncheck →
Helm chart-lint → race-enabled tests → build, plus a printed (not gated)
coverage total on every PR.

### Fixed

- A clean shutdown was recording a fabricated `partial_non_converging`
  outcome for every run, regardless of what the window actually observed
  — an early return after `Watch` in `transport.go` closes it, pinned by
  test.
- `internal/actuate`'s live Kubernetes path (`Exec`, `Patch`,
  `GetConfigMapKey`, pod selection, the multi-step sequence collapse) went
  from effectively untested to covered — the highest blast-radius code in
  the tree, and the file a first-time contributor is pointed at.

### Known gaps

Not in 0.1.0, on purpose:

- No real infrastructure integration beyond what's cataloged; the LLM's
  autonomy is bounded to a fixed action set and a `MaxSteps` loop limit.
- Deferred by design: a production `Model` client, governance/authority
  checks inside clank (that's hiss's job), a risk shaper, rattle-side
  signal validity checks, learning-loop writes, parallel decisions, and
  real (non-fake) source backends.
- Public docs (`docs/architecture.md`, `docs/invariants.md`,
  `docs/onboarding.md`) are still provisional ports from the internal
  design vault, banners and all — a full rewrite for a reader with no
  vault context is the next release's job.

[0.1.0]: https://github.com/IanEff/thump/releases/tag/v0.1.0
