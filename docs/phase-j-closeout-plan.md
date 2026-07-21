# Phase J Closeout — Land the Pile, Fix What Live Found, Reconcile the Docs

**What this is:** the successor to `docs/phase-j-implementation-plan.md`. That plan
was written *before* the live session (Runs 1–6, all closed — see
`docs/phase-j-live-session-notes.md`), and the session overtook most of it. Most of
J0/J1/J2 is already built, tested, and green in the working tree — just uncommitted
(the `feat/calibrations` pile). The old plan still calls that work "build-next," so it
reads like we're stuck when we're mostly done. This doc is the honest, current map of
the small work that actually remains.

**One-sentence status:** the machine converged to `success` twice live (product-catalog
Run 1, cart Run 2), the hold→approve→resume governance round-trip is proven (Run 5), and
the confidence/observability plumbing landed and live-verified — what's left is landing
the green pile as discrete PRs, two small fixes Run 5 surfaced, and a couple of doc
corrections.

**Ground rules:** never auto-commit (each PR is Ian's to land); `go-standards` skill
before any `.go` edit; ACE test names + `cmp.Diff(want, got)` + map-based tables + house
godoc voice (AGENTS.md).

---

## Status at a glance (read this first after any gap)

| Wave | What | State | Blast radius |
|---|---|---|---|
| **K0** | Land the green pile as discrete PRs | ⬜ green, uncommitted | commit hygiene, no new code |
| **K1** | `trim approve` reports "published," not "granted" (Bug B) | ⬜ | `internal/trim/trim.go` + test |
| **K2** | Park `accelerate-recovery` no-op + reversal-on-success gap (Bug A) | ⬜ | vault docs + 1 code comment |
| **K3** | Cross-domain gate hardening (optional) | ⬜ | `internal/clank/gate_test.go` |
| **K4** | Doc reconciliation (click ≠ zero-code; retire stale plan) | ⬜ | CLAUDE.md ×2, docs/ |
| **K5** | Calibrate floors from real data | 🔒 blocked on ≥5 live samples | `config/hiss/policy.yaml` |

### Already done and green in the working tree (do not re-plan)

J1 confidence persistence (`ConfidenceFor` + `reasoned` slog + `Message.ToolCalls` +
tests, live-proven Run 1) · J2 trim gaps (force-on-non-held, `$USER` default,
Tick-accumulation tests) · `trim sync` NATS bridge · cart Redis/Valkey classification
fix · otel-demo read RBAC · budget-aware seed prompt + `MaxSteps` 8→10 · O-2 comment
correction · rook-gce-k3s temp-block removal. Verified green: `go test` on
clank/trim/contract/proposal/hiss.

### Out of scope / deferred

NATS mTLS — in-cluster plaintext is fine for the rig; the model API (HTTPS via the SDK)
and S3 WAL (HTTPS) already use CA-trusted TLS we don't manage. Revisit when beats
modularize into independent binaries over a shared broker; it's a divergence-ledger row
then, not a wave now. · `squawk` (second operator surface) · a third domain · a
Rook-operator-pause action (rig-repo, not thump) · K5 auto-tuning — the operator edits
the floor, never a script (I-3).

---

## Wave K0 — Land the green pile 📦 (no new code)

**Why:** the pile is one ~481-line uncommitted blob; that's the actual source of
"stuck." Carving it into individually-green PRs is the fix. This wave is a *map* — each
PR below is already green on its own.

**PR split:**

1. **`clank`: persist recommended confidence + budget-aware loop** —
   `api/v1/proposal/proposal.go` (+`proposal_test.go`),
   `internal/clank/{engine,store,wiring}.go`, `internal/clank/{checkpoint,reasoned}_test.go`.
   (Folds J1 + seed-prompt budget + `MaxSteps`→10; both touch `engine.go`, so one PR
   avoids hunk-splitting.)
2. **`contract`: correct cart failure classification** —
   `internal/contract/failureclass.go`, `config/actions/{failure-classes,catalog}.yaml`,
   `internal/contract/authored.go` (cart description + O-2 comment). Zero catalog-scope
   change — `ApplicableFailureClasses` untouched (the near-miss Ian caught in Run 2:
   binding an unscoped action to `dependency_saturation` would make it proposable for an
   unrelated Ceph signal).
3. **`deploy`: otel-demo read RBAC for clank's investigation loop** —
   `deploy/chart/thump/templates/rbac-otel-demo-read.yaml` (+ actuate comment). Fold in
   the `tilt-values-rook-gce-k3s.yaml` temp-block removal (both deploy-only).
4. **`trim`: NATS sync bridge + `--nats-url` on approve/force** —
   `internal/trim/{sync,trim}.go`, `internal/trim/{sync,trim}_test.go` (carries the J2
   gap tests, same file).
5. **`hiss`: drop vestigial floors** — `config/hiss/policy.yaml`. Decision: keep the
   removal. Floors gate proposals; with no catalogued action naming
   `dependency_saturation`/`resource_exhaustion`, there is never a proposal for those
   classes, so the floor is never consulted — dead config. (Run 2 *classified* a signal
   as `dependency_saturation`, but with no bound action the model called `insufficient`;
   no proposal was ever governed.)

**Do NOT commit:** `deploy/tilt-values-thump-test.yaml` — the session-only `⚠️ TEMPORARY`
arm block. Revert it before merge (O-9).

**Done when:** the pile is landed as ~5 PRs, each `task ci`-green, temp block reverted.

---

## Wave K1 — `trim approve` tells the truth 🔎 (Bug B, TDD)

**Why:** Run 5 proved `trim approve` prints identical `approved <fp> as <who>` whether
hiss granted it or rejected it server-side (`WARN approval arrived for an unheld
fingerprint`). It fire-and-forgets to `thump.approvals` and never reads the verdict —
the CLI claims a grant that may not have happened. Decision: reword to state what
actually happened (published, async), not read the verdict back.

**Touch:** `internal/trim/trim.go` `runApprove`.

**Test (red first)** in `internal/trim/trim_test.go`:

```go
// TestMain_ApproveReportsPublishedNotGranted pins that approve's success line
// says the approval was *published* (async) and never claims a grant — hiss,
// not trim, decides whether an unheld or stale fingerprint is actually
// approved, so a success line implying otherwise misleads the operator.
```

The existing `TestMain_ApprovePublishesAnAuditableApproval` and
`TestMain_ApprovePublishesToNATSWhenNATSURLIsSet` assert the old "approved … as …"
text — updating their expectation is the predicted red.

**Production:** reword the stdout line to state publication, not a grant, e.g.
`published approval for <fp> as <who> — async; watch 'trim incidents' or hiss logs for the grant`.
Scope to `approve` only — `force` bypasses the gate by construction, so its
`FORCED … — bypassed hiss's risk gate` line is already honest.

**Done when:** `go test ./internal/trim -run Approve -v` green; no approve output implies
a grant occurred.

---

## Wave K2 — Park `accelerate-recovery` honestly 🅿️ (Bug A, docs)

**Why:** Run 5 surfaced two things worth writing down.

1. **`accelerate-recovery` is a no-op on thump-test.** Rook's operator reconciles
   `spec.cephConfig.osd.osd_max_backfills`/`osd_recovery_max_active` back to the
   CR-declared `"1"` within ~29ms of thump's `ceph config set` (measured via `ceph
   config log`). thump's own logic is correct — the actuation is defeated externally. A
   real live test needs the Rook operator paused for the mutation window
   (`kubectl -n rook-ceph scale deploy/rook-ceph-operator --replicas=0`), a rig-repo
   concern.
2. **A reversal-on-success modeling gap.** `ReversalWatcher.Watch`
   (`internal/thump/reversal.go`) fires the undo *only on non-convergence*. That's right
   for a *corrective* action like `disable-*-failure` (whose mutation is the desired
   steady state), but a *transient tuning* action like `accelerate-recovery` (whose boost
   is meant to be temporary) never restores its knobs after success. Real, but non-urgent
   and moot on this rig — parked, not fixed.

**Touch (vault):** a conscious-divergence note in `thump-charter.md` §5 for the Rook
no-op, and a `thump-running-notes.md` entry for the transient-vs-corrective reversal
distinction.

**Touch (repo, no behavior):** one comment near `authored.go`'s `accelerate-recovery`
contract pointing a future reader at the running-notes entry, so the no-op isn't
re-discovered from scratch.

**Done when:** both facts are written where the next session will see them; no code
behavior changes.

---

## Wave K3 — Cross-domain gate hardening 🧪 (optional, TDD)

**Why:** Run 3's argocd decline was correct *on the merits* (the model called
`insufficient`) but never exercised the `anyCoherentLive`/`Subject`-mismatch gate the
cross-domain fix targets — so that mechanism has no dedicated test. The old plan's
gate-edge gaps also remain (only the Upstream path is tabled). Low-stakes; pick up on a
low-energy day.

**Touch:** `internal/clank/gate_test.go` `TestGate` (map-based table). Each a table case
with an ACE key:

- Subject-mismatch probe — a Live `EvidenceRef` cross-domain-tagged (Subject not in the
  SAO topology) that would otherwise look actionable → `anyCoherentLive` attenuates →
  gate fails. (The test Run 3 did not provide.)
- Downstream topology match (only Upstream is tabled today).
- Two cross-domain refs, both out-of-topology → `anyCoherentLive` false.
- Non-nil-but-empty topology (`Topology: &Topology{}`) → every tagged ref fails.
- Reason priority when both `dedupeOK=false` and `evidenceOK=false` → assert which reason
  wins by switch order.

**Done when:** `gotestdox ./internal/clank | grep -i gate` reads as a full spec of the
cross-domain gate; `go test ./internal/clank` green.

---

## Wave K4 — Doc reconciliation 📚 (mostly non-code)

**Why:** the docs drifted from what the session proved.

**Touch:**

- Root `~/projects/go/CLAUDE.md` **and** repo `CLAUDE.md`: **click is not zero-code.**
  `internal/clank/click.go` + the `Recorder` in `metrics.go` run live in broker mode —
  this session's `agent_resolutions_total`/calibration samples are direct proof. Correct
  both files' Trajectory / deferred-things framing.
- `authored.go` (1 comment): note the `ObservedSeverity` 5m-vs-2m windowing lag near the
  O-2 comment, so a future reader doesn't chase `ObservedSeverity=0.4`-at-success as a
  regression — it's the 5m severity gauge rolling off while the 2m convergence query
  already read 0.
- `docs/phase-j-implementation-plan.md`: mark J1/J2 done, J3-Part-B decided (floors
  removed), and point forward to this closeout doc.

**Done when:** no doc still claims click is zero-code or J1 is open work.

---

## Wave K5 — Calibrate floors from real data 🎯 (BLOCKED — do not start)

**Why blocked:** J3 Part A wants the `service_failure` floor set from real
`agent_proposal_success_total{confidence_bucket,success}` data — needs ≥5 samples per
class. We have ~2 real `service_failure` successes (product-catalog, cart). Park until
more live runs accumulate. **Never auto-tune** — the operator reads the metric and edits
the YAML; a script rewriting `policy.yaml` is the agent writing its own permission slip
(I-3).

**When samples exist**, the PromQL to read:
`agent_proposal_success_total{confidence_bucket="0.8-0.9"}` vs `{...="0.7-0.8"}`; if the
0.8–0.9 bucket is ≥80% success, the floor can rise to 0.8 with a dated, evidence-cited
comment.

---

## Live-cluster teardown (do before walking away from the session)

Operational, not code — but easy to forget:

- `ceph balancer on` (turned off mid-session for a clean convergence signal).
- `kubectl -n chaos-mesh delete podchaos osd-pod-failure-autonomous` (CR lingers,
  `AllRecovered: true`).
- Revert `deploy/tilt-values-thump-test.yaml`'s temp arm block (also K0's exclusion).
- `just destroy` (or leave up if more live work is planned).

---

## Verification

- **Per PR (K0):** each lands `task ci`-green (fmt-check → vet → lint → vulncheck →
  chart-lint → race → build). The pile already passes targeted `go test`; the gate is
  lint + chart-lint, not just tests.
- **K1:** `go test ./internal/trim -run Approve -v` green; eyeball `trim approve`'s new
  wording.
- **K3:** `go test ./internal/clank -run TestGate -v` + `gotestdox ./internal/clank`.
- **Whole branch before merge:** `task ci` green on `feat/calibrations`; temp arm block
  reverted; `gotestdox ./...` reads clean.
- **No regressions in the deferred set:** click stays wiring (not a new binary); no
  policy/authority logic creeps into clank; no `os/exec`/`net` into package `thump`.
