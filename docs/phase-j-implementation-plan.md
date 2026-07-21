# Phase II — Prove the Operator, Close the Loop

**What this is:** the guide that follows Phase I. Phase I grew the catalog from
runbooks. Phase II makes the operator real, proves the full loop end-to-end, and
starts the calibration data flowing. The machine has auto-approved and executed
twice; it has **never converged to `success`**, and the operator has never seen it
work through `trim`. Phase II is where both of those change.

**One-sentence status:** thump auto-approved and executed
`disable-product-catalog-failure` twice live on `thump-test` (2026-07-20), but
both runs settled `partial_non_converging` — the first from a converge-parser bug
(fixed, `167de2d`), the second from a structural timing gap (the 5-minute
`rate()` window can't settle inside a 5-minute convergence `Window`). A
cross-domain misclassification was found, diagnosed, and fixed in the same session
(`0c089e0`). `trim` is built and unit-tested but has never been pointed at a real
cluster. The approval consumption path in hiss (`approveHandler`,
[transport.go:129](file:///Users/ian/projects/go/thump/internal/hiss/transport.go#L129))
is built, tested (3 ACE cases in
[approve_test.go](file:///Users/ian/projects/go/thump/internal/hiss/approve_test.go)),
and wired into
[hiss.go:131-133](file:///Users/ian/projects/go/thump/internal/hiss/hiss.go#L131-L133)
via JetStream — but has never fired live. The calibration metrics
(`agent_proposal_confidence`, `agent_proposal_success_total`,
`agent_action_effectiveness_delta`) are instrumented in
[metrics.go](file:///Users/ian/projects/go/thump/internal/clank/metrics.go) and
tested, but **no real sample has ever been observed** because no outcome has ever
reached `success`. Phase I is closed in code; Phase II is where it proves out in
operation.

---

## §1 — Phase I Ledger (what carries forward)

### ✅ Landed and proven

| Item | Evidence | Commit |
|---|---|---|
| 5 actions in catalog, all reversible, all bound | `TestShippedCatalogMatchesAuthoredDefault` green | through `2ce616d` |
| Auto-approval of `disable-product-catalog-failure` (twice) | `hiss decision verdict=approved grantedBand=act_reversible` live on thump-test | uncommitted arm block |
| Live execution (flagd ConfigMap merge-patch) | `thump outcome applied (mode=live)` | same session |
| Converge target parser fix (`==` operator) | `parseTarget` regex `(<\|==)\s*([\d.]+)` | `167de2d` |
| Cross-domain evidence gate | `EvidenceRef.Subject` + `anyCoherentLive` + 4 new gate test cases | `0c089e0` |
| Subject annotations on all OTel + ArgoCD evidence queries | 8 queries tagged in evidence-queries.yaml | `0c089e0` |
| Dead knobs demoted (`throttle`, `scale-out`) | `authored.go` + `catalog.yaml` + bindings | `0fff1b2` |
| `trim` CLI: `incidents`, `approve`, `force` | 7 CLI tests + 14 fold subtests + 8 transport tests + 5 render tests + 6 projection tests | `2add8ef`, `5edbcf9` |
| hiss approval consumption (`approveHandler`) | 3 ACE tests, JetStream subscription wired | `5edbcf9` |
| `PendingHolds` store — in-memory, Take-once, rebuildable | [pending.go](file:///Users/ian/projects/go/thump/internal/hiss/pending.go) | `5edbcf9` |
| `service_failure` class with 3 OTel demo actions | `disable-product-catalog-failure`, `disable-cart-failure`, `restart-cart-pod` | `fd1edb6`, `2ce616d` |
| O-1 timing fix: `rate()` shortened to `[2m]` | [evidence-queries.yaml:132-140](file:///Users/ian/projects/go/thump/config/thump-test/whir/evidence-queries.yaml#L132-L140) — `product_catalog_error_ratio` and `cart_error_ratio` both `[2m]` | `700209f`, `2a5efc8` |

### ⬜ Open — carries into Phase II

| # | Item | Root cause | Recommended fix | Owner |
|---|---|---|---|---|
| **O-1** | ⚠️ `Window` in authored.go still `5*time.Minute` | The `rate()` window is shortened to `[2m]` (committed `700209f`/`2a5efc8`) but `SuccessCriteria.Window` in [authored.go:101](file:///Users/ian/projects/go/thump/internal/contract/authored.go#L101) and [authored.go:132](file:///Users/ian/projects/go/thump/internal/contract/authored.go#L132) is still `5*time.Minute`. The 2m rate window means the metric can settle inside a 5-minute convergence window (2m + propagation ≈ 3m), so the `Window` *may* not need widening — but this hasn't been proven live. **Verify in the next live session: if `success` converges under the current settings, the Window is fine; if it doesn't, widen to `8*time.Minute`.** | Ian — live verification |
| **O-2** | `authored.go:102-105` comment claims "~40-60s" recovery | Conflates HTTP-symptom clearing with the `rate()` metric settling | Correct once real settle timing is re-verified under the `[2m]` rate window | Ian |
| **O-3** | Cart failure path not live-tested | Chose product-catalog for first run; session cut on cost | Verify in the next live session (same chaos shape, different flag) | Ian |
| **O-4** | ArgoCD decline-probe not re-verified post-fix | Cross-domain fix landed after session; `rook-cluster OutOfSync` was the live trigger | Re-inject (or let `rook-cluster` stay `OutOfSync`) and confirm clank declines | Ian |
| **O-5** | `trim` never used against a live cluster | Just built; no cluster was up at ship time | Run `trim incidents --inbox <dir>` against the live stream during the next session | Ian |
| **O-6** | Hold→approve→resume never fired live | `accelerate-recovery` always held; trim approve + hiss approveHandler never exercised E2E | Inject a `redundancy_degraded` signal, watch hiss hold, `trim approve`, confirm thump executes | Ian |
| **O-7** | Confidence float not persisted in any durable artifact | Transcript checkpoint captures `Msgs`, not terminal `propose` tool call args; `decision` slog line now carries `confidence`/`floorApplied` but slog is ephemeral | See J1 below — structured in the plan | Code |
| **O-8** | Calibration metrics have zero real samples | No `success` outcome → `recordCalibration`/`recordEffectiveness` never fire live | Get O-1's `success`, then the metrics populate automatically | Blocked on O-1 |
| **O-9** | `⚠️ TEMPORARY` arm blocks in both tilt-values files | Uncommitted live-executor config, dated 2026-07-18 / 2026-07-20 | Remove after the live session, before any merge | Ian |

---

## §2 — Live Test Plan (one efficient session on `thump-test`)

> **Constraint:** GCE cluster costs money. Plan one session, batch the
> verifications, quiesce between injections.

### Pre-flight (before any chaos)

1. **Verify the O-1 timing fix is committed and deployed.**
   The `rate()` window is already `[2m]` in
   [evidence-queries.yaml:132-140](file:///Users/ian/projects/go/thump/config/thump-test/whir/evidence-queries.yaml#L132-L140)
   (committed `700209f`, `2a5efc8`). The `SuccessCriteria.Window` in
   [authored.go:101](file:///Users/ian/projects/go/thump/internal/contract/authored.go#L101) is still
   `5*time.Minute`. The 2m rate window settles faster than the 5m convergence
   window — this *should* be enough. If Run 1 below still produces
   `partial_non_converging`, widen `Window` to `8*time.Minute` and rerun.

2. **Remove the `⚠️ TEMPORARY` arm blocks** from both
   [tilt-values-thump-test.yaml](file:///Users/ian/projects/go/thump/deploy/tilt-values-thump-test.yaml)
   and
   [tilt-values-rook-gce-k3s.yaml](file:///Users/ian/projects/go/thump/deploy/tilt-values-rook-gce-k3s.yaml).
   Re-add a fresh arm block for this session only, uncommitted.

3. **`just up` → `tilt up -- --cluster=thump-test`** → all 4 beats Running, ArgoCD healthy.
   Verify armed live: `kubectl get deployment thump -n thump -o jsonpath='{.spec.template.spec.containers[0].env}'`
   (don't trust the file — chaos-testing skill's own rule).

4. **Baseline quiesce.** Confirm `ceph -s` HEALTH_OK, all OTel demo pods Running, Prometheus
   port-forward up. No active chaos. Wait for any stale `dedupeWindow` fingerprints to expire
   (10 min if you kept that setting).

### Run 1 — Product-catalog `success` convergence (O-1 proof)

**Goal:** the Phase I definition-of-done screenshot: `applied → settled result=success reversed=false`.

```
chaos/flag-product-catalog-on.sh          # inject
watch for: hiss decision verdict=approved
watch for: thump outcome applied
watch for: thump settled result=success   # ← the new one
```

**What to verify after `success`:**
- `ObservedSeverity` near 0 (the metric actually settled)
- `reversed=false` (success = no reversal)
- In Prometheus: `agent_action_effectiveness_delta` has a sample (the AE datum)
- In Prometheus: `agent_proposal_success_total{success="true"}` incremented

**Quiesce:** `chaos/flag-product-catalog-off.sh`, wait for the dedupe window to clear.

### Run 2 — Cart failure path (O-3)

**Goal:** second domain action, same `success` shape, proves cart-specific queries + `restart-cart-pod`
discrimination.

```
# Cart chaos equivalent (whatever the rig's cart-failure injection is — likely a flagd flag)
watch for: hiss decision → approved, contractRef=disable-cart-failure (NOT restart-cart-pod)
watch for: thump settled result=success
```

**What to verify:**
- The ranker chose `disable-cart-failure` (SeverityReductionPct=0.9) over `restart-cart-pod` (0.1)
- The subject annotations (`subject: cart`) threaded correctly through the evidence gate
- AE delta near 0

**Quiesce:** restore baseline, wait for dedupe.

### Run 3 — ArgoCD decline-probe (O-4, cross-domain fix verification)

**Goal:** confirm the cross-domain gate produces an honest decline, not the misfire from 2026-07-20.

If `rook-cluster` is still genuinely `OutOfSync`:
- The `argocd-sync` signal will fire on its own
- clank should reason, hit the evidence gate (OTel queries carry `subject: product-catalog`/`cart`,
  ArgoCD topology doesn't contain those nodes), and **decline** — no `proposed` phase, or a
  `proposed` whose `gatePassed=false`

If `rook-cluster` is synced: skip this run — it's a naturally-occurring signal, not injectable.

**What to verify:**
- clank logs show `gatePassed=false` or a clean decline with reason
- No `thump outcome` line for an argocd-sync fingerprint
- The transcript (S3) shows the model queried cross-domain metrics but the gate attenuated them

### Run 4 — `trim` live proof (O-5)

**Goal:** prove the operator surface reads real state.

Run concurrently with Runs 1-3 (trim is read-only):

```bash
# Point trim at the NATS inbox (or the filesystem inbox if dir-transport)
trim incidents --inbox <path-to-inbox>
trim incidents --inbox <path-to-inbox> --json | jq .
```

**What to verify:**
- `trim incidents` shows real incidents with correct stages (`detected`, `proposed`, `approved`, `applied`, `settled`)
- Severity values are real (not nil, not unmeasured for settled incidents)
- Held incidents (if any) show `held <duration>`
- `--json` output is parseable and matches the human output

### Run 5 — Hold→approve→resume (O-6)

**Goal:** prove `trim approve` → hiss `approveHandler` → thump executes, end-to-end.

This needs a **held** action. `accelerate-recovery` is `BlastHigh` → always held. To trigger it:

```bash
# Inject an OSD failure (the hold-rebalance / accelerate-recovery trigger)
# Use osd-pod-failure-autonomous.yaml with mon_osd_down_out_interval=300
kubectl apply -f chaos/osd-pod-failure-autonomous.yaml
```

Watch for: `hiss decision verdict=hold reasons=[risk_ceiling]` for an `accelerate-recovery` proposal.

Then:
```bash
trim approve <fingerprint> --approver ian
```

Watch for:
- hiss logs `approved signalRef=<fp> approver=ian grantedBand=<band>`
- thump logs `outcome applied` for `accelerate-recovery`

> **⚠️ WARNING:** `accelerate-recovery` sets `osd_max_backfills=16` and
> `osd_recovery_max_active=16` on a 3-OSD lab cluster. This is safe (it
> accelerates an already-triggered rebalance), but confirm ceph-health
> reconverges afterward. The reversal (`ceph config rm osd osd_max_backfills`,
> `rm osd_recovery_max_active`) fires automatically on the convergence window.

**What to verify:**
- The approval flowed through JetStream (`thump.approvals` → hiss → `thump.decisions`)
- `trim incidents` now shows the incident as `approved` with `approver: ian`
- thump acted on the re-issued decision
- `PendingHolds.Take` consumed the hold (a second `trim approve` for the same fingerprint is inert)

### Run 6 — `trim force` (optional, if time/cost allow)

Reproduce a held action (same as Run 5), then instead of `trim approve`:

```bash
trim force <fingerprint> --operator ian
```

**What to verify:**
- `trim incidents` shows `FORCED by ian` in red
- thump acted on the forced decision
- The forced decision carries `Forced=true`, `Operator=ian`, is `Auditable()`

### Teardown

1. Restore all baselines (`chaos/flag-product-catalog-off.sh`, Ceph `unset noout` if set, `ceph config rm` if accelerate-recovery fired)
2. Remove the `⚠️ TEMPORARY` arm block from `tilt-values-thump-test.yaml`
3. `just destroy` (or leave up if more work is planned)
4. Commit the O-2 comment correction and the O-9 cleanup

---

## §3 — Phase II Code: What to Build Next

### The thesis

Phase I grew the catalog. Phase II makes the machine **observable and calibratable**.
Today the operator's only view is grepping slog output and pulling S3 transcripts
with `aws --endpoint-url`. The calibration metrics exist but have never been
populated. The confidence the model reports is ephemeral — it vanishes with the pod.
Phase II is where the machine becomes something you can **see**, **steer**, and
**learn from** without shelling into a cluster.

### What Phase II is NOT

- **Not a new beat.** All five are built.
- **Not a new operator surface.** `trim` is the operator surface; Phase II proves
  it and hardens it, not replaces it. `squawk` (real-time notifications) is a
  natural follow-on but is **out of scope** — one surface proven live before a
  second is started.
- **Not a new domain.** Two domains (Ceph + OTel demo) are enough signal
  diversity; adding a third is expansion, not foundation.

### The four waves

| Wave | Name | Blast radius | Depends on |
|---|---|---|---|
| **J0** | Phase I closure (config) | tilt-values, authored.go comments | Nothing |
| **J1** | Confidence persistence (post-terminal checkpoint) | `internal/clank/engine.go`, `store.go` | J0 (needs a `success` to verify) |
| **J2** | `trim` hardening + live-proof carry | `internal/trim/` | §2 live session findings |
| **J3** | Calibration read-path + vestigial floor cleanup | `internal/hiss/`, `config/hiss/` | J1 + real AE data (5 samples minimum) |

---

### Wave J0 — Phase I closure 🧹 (config, no structural Go)

**Blast radius: two tilt-values files, one comment. The O-2/O-9 items from §1.**

The O-1 timing fix (`rate()` shortened to `[2m]`) is already committed
(`700209f`, `2a5efc8`). What remains:

1. **Correct the misleading comment** in
   [authored.go:102-105](file:///Users/ian/projects/go/thump/internal/contract/authored.go#L102-L105).
   The current comment says "flag off → ratio back to 0 within ~40-60s."  This
   conflates the HTTP-symptom clearing with the `rate()` metric settling. With
   the `[2m]` rate window, the metric can only settle once 2 full minutes of
   clean requests have been scraped — not 40-60s. Rewrite to:
   ```go
   // VERIFIED LIVE 2026-07-19 (Wave 5): HTTP errors clear ~40-60s after
   // the ConfigMap patch propagates, but the rate([2m]) convergence query
   // needs a full 2-minute clean window to read 0. Total settle time is
   // therefore ~propagation + 2m, well inside the 5-minute Window below.
   ```

2. **Remove both `⚠️ TEMPORARY` blocks** from
   [tilt-values-thump-test.yaml:61-73](file:///Users/ian/projects/go/thump/deploy/tilt-values-thump-test.yaml#L61-L73) and
   [tilt-values-rook-gce-k3s.yaml:71-82](file:///Users/ian/projects/go/thump/deploy/tilt-values-rook-gce-k3s.yaml#L71-L82).

**Done when:** `task ci` green (modulo the sandbox-gated network tests that require
NATS/Prometheus), the comment is honest, the temp blocks are gone.

---

### Wave J1 — Confidence persistence 📊 (post-terminal checkpoint)

**Blast radius: `internal/clank/engine.go`, `internal/clank/store.go`, slog line.
Depends on J0.**

The running notes (2026-07-18 part 8) identified a real gap: *"the exact number
that tripped `confidence_floor` is not recoverable from any persisted artifact."*
The issue is structural:

1. The transcript checkpoint (`Store.Checkpoint`) has exactly one call site in
   the engine — [engine.go:154](file:///Users/ian/projects/go/thump/internal/clank/engine.go#L154)
   (confirmed via gopls `go_symbol_references`: 14 total refs, only line 154 is
   in production code; the rest are tests and the heartbeat). It runs **after**
   `msgs = append(msgs, comp.Message)` at line 153, so it *does* capture the
   assistant message — but **before** the tool call dispatch loop processes the
   current completion.

2. When the model calls `propose` (the terminal tool call), the loop
   [unmarshals the Set at engine.go:174-181](file:///Users/ian/projects/go/thump/internal/clank/engine.go#L174-L181),
   sets `proposed = true` and `done = true`, then breaks — but no second
   checkpoint happens. The `Candidate.Confidence` values the model supplied are
   now on `set.Proposals` and will ride through to `thump.proposals` via
   `Pub.Publish` and to the ledger via `Ledger.Record`, but the **transcript**
   never captures them.

3. The `decision` slog line in hiss
   ([transport.go:106-109](file:///Users/ian/projects/go/thump/internal/hiss/transport.go#L106-L109))
   already carries `confidence` and `floorApplied`, but slog is ephemeral — it
   dies with the pod.

**Decision: post-terminal checkpoint (option a).** A second `Store.Checkpoint`
after the terminal tool call, before the set is published. This captures the
full conversation including the `propose` arguments, so the S3 transcript is
debugging-complete.

#### Implementation steps

**Step 1 — Add the post-terminal checkpoint in `engine.go`.**

After the inner `if done { break }` at
[engine.go:207-209](file:///Users/ian/projects/go/thump/internal/clank/engine.go#L207-L209),
but still inside the outer loop body, the checkpoint already ran at the top of
the iteration (line 155). The terminal tool call's response is never appended to
`msgs` (because `propose` and `insufficient` don't produce a tool-response
message — they break immediately). To capture the model's completion that
*contains* the terminal tool call, we need a checkpoint **after** the outer loop
exits and only when `proposed || declined` — the final `msgs` slice already has
the assistant message with the `propose`/`insufficient` call appended at line 153.

Add a post-loop checkpoint between the loop exit (line 210) and the `set.Evidence`
assignment (line 211):

```go
// Post-terminal checkpoint: the model's final assistant message carrying
// the propose/insufficient tool call was appended to msgs at line 153
// but Checkpoint at line 155 ran *before* that append took effect on the
// next iteration — and there is no next iteration. This captures the
// complete conversation including the terminal call's arguments (in
// particular, Candidate.Confidence), so the S3 transcript is
// debugging-complete. Cost: one extra S3 PUT per reason run.
if proposed || declined {
    if err := e.Store.Checkpoint(ctx, Turn{RunID: runID, Step: step + 1, Msgs: msgs}); err != nil {
        return proposal.Set{}, fmt.Errorf("post-terminal checkpoint: %w", err)
    }
}
```

Wait — re-reading the loop: `msgs = append(msgs, comp.Message)` happens at line
153, **then** `Checkpoint` at line 155 runs with the updated `msgs`. So the
checkpoint at 155 *does* include the assistant message containing the `propose`
tool call. The issue is that `comp.Message` contains the raw tool call JSON (the
`ToolCalls` field on the `Completion`), which `Checkpoint` persists as part of
`Msgs`. So the confidence *is* in the checkpoint, serialized inside the
assistant message's tool call arguments.

Let me re-read the gap claim more carefully. The `Turn.Msgs` contains
`[]Message`, and `Message` has `Role` and `Content`. The `Completion` struct
([store.go:241-258](file:///Users/ian/projects/go/thump/internal/clank/store.go#L241-L258)) —
the `comp.Message` appended at line 153 is of type `Message`, which is `{Role,
Content}`. If the `Content` field carries the raw JSON of the tool call
arguments, then the confidence *is* checkpointed. But if `Content` only carries
the model's natural-language content and the tool call arguments are in a
separate `ToolCalls` field that isn't part of `Message`, then the gap is real.

Looking at [store.go:241-258](file:///Users/ian/projects/go/thump/internal/clank/store.go#L241-L258):
`Message` is `{Role string, Content string}` and `Completion` is
`{Message, ToolCalls []ToolCall}`. The `comp.Message` appended to `msgs` at
line 153 contains only `Role` + `Content` — the `ToolCalls` are a sibling field
on `Completion`, not part of `Message`. **The gap is confirmed:** the checkpoint
captures the model's natural-language response but not the structured tool call
arguments where `Candidate.Confidence` lives.

So: a post-terminal checkpoint won't help either, because the checkpoint saves
`msgs` (a `[]Message` with `{Role, Content}` only), not the raw `Completion`s
with their `ToolCalls`. The fix needs to either:

**(a) Enrich `Message` to carry tool call arguments,** so `Checkpoint` captures
them. This means either adding a `ToolCalls` field to `Message` (changing the
`Turn` wire format) or serializing the tool call JSON into `Content`.

**(b) Add the confidence to the `reasoned` slog line as an immediate cheap fix,**
and document that the `proposal.Set` on `thump.proposals` (the boundary object,
the system of record per I-11) is the durable artifact carrying the confidence.

**Recommendation: do both.** Layer 1 is cheap and immediate; Layer 2 makes the
transcript genuinely complete.

#### Layer 1 — Log the confidence on the `reasoned` slog line

The deferred `reasoned` log at
[engine.go:115-118](file:///Users/ian/projects/go/thump/internal/clank/engine.go#L115-L118)
currently carries `recommended`, `contractRef`, `proposals`, `evidence`,
`gatePassed`, and `reason`. It does **not** carry the recommended candidate's
`Confidence` float.

**Change:** After the named return `set` is populated, the deferred closure
already reads `set.Recommended`. Add `confidence` by finding the recommended
candidate's `Confidence`:

```go
slog.Info("reasoned", "run_id", runID, "fingerprint", sig.Fingerprint, "step", step, "phase", phase,
    "recommended", set.Recommended, "contractRef", set.ContractRefFor(set.Recommended),
    "proposals", len(set.Proposals), "evidence", len(set.Evidence),
    "gatePassed", set.Gate != nil && set.Gate.Passed, "reason", set.Status.Reason,
    "confidence", set.ConfidenceFor(set.Recommended))
```

This requires a `ConfidenceFor` helper on `proposal.Set` (the same pattern as
the existing `ContractRefFor` at
[proposal.go:38-45](file:///Users/ian/projects/go/thump/api/v1/proposal/proposal.go#L38-L45)):

```go
// ConfidenceFor returns the Confidence of the Proposals entry whose ID
// matches candidateID, or 0 if none matches — the same lookup pattern as
// ContractRefFor, used by slog lines that need the float without scanning
// Proposals themselves.
func (s Set) ConfidenceFor(candidateID string) float64 {
    for _, c := range s.Proposals {
        if c.ID == candidateID {
            return c.Confidence
        }
    }
    return 0
}
```

> [!NOTE]
> **This consolidates two existing private helpers found via gopls.** The same
> lookup already exists in two places:
> - `recommendedConfidence` in
>   [metrics.go:92-98](file:///Users/ian/projects/go/thump/internal/clank/metrics.go#L92-L98)
>   (returns `(float64, bool)`, called by `recordCalibration`)
> - `recommended` in
>   [authority.go:128](file:///Users/ian/projects/go/thump/internal/hiss/authority.go#L128)
>   (returns the full `Candidate`, called by `Evaluate` and `transport.go:105`)
>
> Moving the lookup to the `Set` type itself is the "accept interfaces, return
> structs" corollary: the boundary object owns its own accessor. Once
> `ConfidenceFor` lands on `Set`, the two private helpers can delegate to it
> (or be inlined), eliminating the duplication.

#### Layer 2 — Post-terminal checkpoint with enriched Turn

Extend `Message` in [store.go](file:///Users/ian/projects/go/thump/internal/clank/store.go)
to carry an optional `ToolCalls` field:

```go
type Message struct {
    Role      string     `json:"role"`
    Content   string     `json:"content"`
    ToolCalls []ToolCall `json:"toolCalls,omitempty"` // present only on assistant messages that dispatched tool calls; the terminal propose/insufficient call's arguments carry Candidate.Confidence and other structured data the Content field alone doesn't capture
}
```

Then in the reason loop, when building the message from `comp` at line 153,
populate `ToolCalls` from `comp.ToolCalls`:

```go
msgs = append(msgs, Message{Role: comp.Message.Role, Content: comp.Message.Content, ToolCalls: comp.ToolCalls})
```

This change means every checkpoint — including the one at line 155 that already
runs after the terminal `propose` completion — will carry the full tool call
arguments, making the S3 transcript debugging-complete without needing a
second checkpoint call.

#### Tests (TDD — write first, then implement)

**Test 1: `TestPropose_ReasonedLogCarriesTheRecommendedCandidateConfidence`**

Location: [engine_test.go](file:///Users/ian/projects/go/thump/internal/clank/engine_test.go)

```go
// TestPropose_ReasonedLogCarriesTheRecommendedCandidateConfidence pins that
// the deferred "reasoned" slog line emits the confidence float from the
// recommended candidate, not 0 or a stale value — the cheap, immediate
// observability layer before the structured checkpoint makes it durable.
func TestPropose_ReasonedLogCarriesTheRecommendedCandidateConfidence(t *testing.T) {
    t.Parallel()
    // Arrange: a model that proposes with a known confidence
    wantConf := 0.87
    model := &fakeModel{script: []Completion{{ToolCalls: []ToolCall{{
        Name: "propose",
        Args: proposeArgs(t, proposal.Set{
            FailureClass: proposal.ClassServiceFailure,
            Hypotheses:   []Hypothesis{{Name: "service_failure", Weight: 0.9}},
            Proposals:    []Candidate{{ID: "p1", ContractRef: "disable-product-catalog-failure", Confidence: wantConf}},
        }),
    }}}}}
    e, logs := newTestEngine(model) // logs = slog test handler

    // Act
    _, err := e.Propose(context.Background(), sigServiceFailure())
    if err != nil {
        t.Fatal(err)
    }

    // Assert: the "reasoned" log line carries confidence=0.87
    // (exact assertion depends on how the test slog handler captures attrs)
    entry := logs.FindByMessage("reasoned")
    if entry == nil {
        t.Fatal("no 'reasoned' slog line emitted")
    }
    gotConf := entry.Float64("confidence")
    if gotConf != wantConf {
        t.Errorf("want confidence=%v on the reasoned slog line, got %v", wantConf, gotConf)
    }
}
```

**Test 2: `TestPropose_CheckpointCapturesTerminalToolCallArguments`**

Location: [checkpoint_test.go](file:///Users/ian/projects/go/thump/internal/clank/checkpoint_test.go)

```go
// TestPropose_CheckpointCapturesTerminalToolCallArguments pins that the
// S3/DirStore transcript contains the propose tool call's structured
// arguments (including Candidate.Confidence) — not just the model's
// natural-language Content. This is the durable artifact the running notes
// (2026-07-18 part 8) identified as missing.
func TestPropose_CheckpointCapturesTerminalToolCallArguments(t *testing.T) {
    t.Parallel()
    store := NewMemStore()
    wantConf := 0.82
    model := &fakeModel{script: []Completion{{ToolCalls: []ToolCall{{
        Name: "propose",
        Args: proposeArgs(t, proposal.Set{
            FailureClass: proposal.ClassServiceFailure,
            Hypotheses:   []Hypothesis{{Name: "service_failure", Weight: 0.9}},
            Proposals:    []Candidate{{ID: "p1", ContractRef: "disable-product-catalog-failure", Confidence: wantConf}},
        }),
    }}}}}
    e, _ := newTestEngine(model)
    e.Store = store

    if _, err := e.Propose(context.Background(), sigServiceFailure()); err != nil {
        t.Fatal(err)
    }

    // The last checkpointed turn's msgs should contain a Message whose
    // ToolCalls include the propose call with the confidence value.
    pending, _ := store.Pending(context.Background())
    // (store will be empty because Finish clears it — inspect the
    // recording store's captured turns instead, or use a spy store.)
    // ... assert that the final turn's assistant message carries ToolCalls
    // with a "propose" entry whose Args contain "confidence":0.82
}
```

**Test 3: `TestSet_ConfidenceForReturnsTheMatchingCandidateConfidence`**

Location: new file in `api/v1/proposal/` or added to an existing test file.

```go
// TestSet_ConfidenceForReturnsTheMatchingCandidateConfidence pins that
// ConfidenceFor returns the named candidate's float, not 0 — the claim
// that the confidence is recoverable from the Set (I-11).
func TestSet_ConfidenceForReturnsTheMatchingCandidateConfidence(t *testing.T) {
    t.Parallel()
    s := Set{
        Proposals:   []Candidate{{ID: "c1", Confidence: 0.85}, {ID: "c2", Confidence: 0.72}},
        Recommended: "c1",
    }
    if got := s.ConfidenceFor("c1"); got != 0.85 {
        t.Errorf("ConfidenceFor(c1): want 0.85, got %v", got)
    }
    if got := s.ConfidenceFor("missing"); got != 0 {
        t.Errorf("ConfidenceFor(missing): want 0, got %v", got)
    }
}
```

**Done when:** (1) the `reasoned` slog line carries `confidence=<float>`,
verified by a test; (2) the checkpoint's `Message.ToolCalls` captures the
terminal `propose` arguments, verified by a test; (3) `ConfidenceFor` exists
on `proposal.Set` with a test pinning it.

---

### Wave J2 — `trim` hardening + live-proof carry 🔧

**Blast radius: `internal/trim/`. Depends on §2 live session findings.**

The live test plan (§2 Runs 4-6) will surface real gaps. This wave absorbs
those findings plus the known gaps identified from the test-coverage analysis.

#### Known gaps (before live)

**Gap 1: `force` on a non-held but existing incident.**

[trim.go:149-152](file:///Users/ian/projects/go/thump/internal/trim/trim.go#L149-L152) checks
`inc.Held == nil`, but the only test covering a force failure
([trim_test.go:198-211](file:///Users/ian/projects/go/thump/internal/trim/trim_test.go#L198-L211))
uses a completely missing fingerprint (`"no-such-fp"`). The `!ok` and
`inc.Held == nil` branches are distinct failure modes that read identically in
the error message ("is not currently held"), but only one is tested.

**Test to write:**
```go
// TestMain_ForceFailsWhenTheIncidentExistsButIsNotHeld pins that force on
// an approved (not held) incident errors cleanly, not crashes — the !ok
// and Held==nil branches are distinct failures with the same message.
func TestMain_ForceFailsWhenTheIncidentExistsButIsNotHeld(t *testing.T) {
    t.Parallel()
    inbox, outbox := t.TempDir(), t.TempDir()
    // Write a decision with VerdictApproved (not VerdictHold) — the incident
    // exists in the projection but is not held.
    approved := decision.Governed{
        Decision: decision.Decision{
            ID: "dec-1", SignalRef: "fp-1", Verdict: decision.VerdictApproved,
            RequestedBand: decision.BandActReversible, // ...
        },
    }
    writeYAML(t, filepath.Join(inbox, "decisions"), "dec-1.yaml", approved)

    var stdout, stderr bytes.Buffer
    code := trim.Main([]string{"force", "fp-1", "--inbox", inbox, "--outbox", outbox}, &stdout, &stderr)

    if code == 0 {
        t.Error("want nonzero exit code for force on a non-held incident")
    }
    if !strings.Contains(stderr.String(), "not currently held") {
        t.Errorf("want 'not currently held' in stderr, got %q", stderr.String())
    }
}
```

**Gap 2: `approve` / `force` `$USER` default.**

Neither
[trim_test.go:101-135](file:///Users/ian/projects/go/thump/internal/trim/trim_test.go#L101-L135)
(approve test) nor
[trim_test.go:148-196](file:///Users/ian/projects/go/thump/internal/trim/trim_test.go#L148-L196)
(force test) exercises the `--approver`/`--operator` flag defaulting to
`os.Getenv("USER")` — both tests pass explicit names. The default is wired at
[trim.go:91](file:///Users/ian/projects/go/thump/internal/trim/trim.go#L91) and
[trim.go:132](file:///Users/ian/projects/go/thump/internal/trim/trim.go#L132).

**Test to write:**
```go
// TestMain_ApproveDefaultsApproverToUSEREnvVar pins that omitting
// --approver uses $USER, not "" — the Auditable() check would catch
// an empty string, but the test pins the default explicitly.
func TestMain_ApproveDefaultsApproverToUSEREnvVar(t *testing.T) {
    t.Parallel()
    outbox := t.TempDir()
    t.Setenv("USER", "testuser")

    var stdout, stderr bytes.Buffer
    code := trim.Main([]string{"approve", "fp-1", "--outbox", outbox}, &stdout, &stderr)

    if code != 0 {
        t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
    }
    // Read back the written Approval and confirm Approver == "testuser"
    matches, _ := filepath.Glob(filepath.Join(outbox, "*.yaml"))
    raw, _ := os.ReadFile(matches[0])
    var got approval.Approval
    yaml.Unmarshal(raw, &got)
    if diff := cmp.Diff("testuser", got.Approver); diff != "" {
        t.Error("wrong default approver (-want +got)", diff)
    }
}
```

**Gap 3: Tick accumulation across multiple polls.**

The transport tests cover single-pass `Tick` and `Snapshot`-over-archived, but
no test drives two sequential `Tick()` calls and confirms cumulative fold. In
production, `trim incidents` uses `Snapshot` (single read), but `Tick` is the
incremental path a long-running trim daemon would use.

**Test to write:**
```go
// TestTick_TwoSequentialTicksFoldCumulativelyIntoTheProjection pins that
// a second Tick incorporates new boundary objects alongside the first's
// — the Projection accumulates, it doesn't reset.
func TestTick_TwoSequentialTicksFoldCumulativelyIntoTheProjection(t *testing.T) {
    t.Parallel()
    inbox := t.TempDir()
    // Tick 1: one detection
    writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
        signal.Detection{Fingerprint: "fp-1", DetectedAt: time.Now()})
    tr := &Transport{Inbox: inbox}
    tr.Tick(context.Background())

    // Tick 2: a second detection arrives
    writeYAML(t, filepath.Join(inbox, "detections"), "det-2.yaml",
        signal.Detection{Fingerprint: "fp-2", DetectedAt: time.Now()})
    tr.Tick(context.Background())

    // Assert: projection contains both
    incidents := tr.Proj.Snapshot()
    if len(incidents) != 2 {
        t.Errorf("want 2 incidents after two ticks, got %d", len(incidents))
    }
}
```

**Gap 4: `trim incidents` with real NATS.**

Today all transport tests use the filesystem inbox
([transport.go:16-19](file:///Users/ian/projects/go/thump/internal/trim/transport.go#L16-L19)).
On the rig, the beats use JetStream. The live session (§2 Run 4) will answer
empirically whether `trim` needs a NATS consumer or whether the filesystem inbox
is sufficient.

**If `trim` needs NATS** (the rig doesn't materialize JetStream to disk):
- Add a `--transport nats` flag to `trim incidents`.
- Implement a `NATSTransport` that subscribes to the four subjects
  (`thump.detections`, `thump.proposals`, `thump.decisions`, `thump.outcomes`)
  and folds into the same `Projection`.
- The filesystem transport stays as the testscript / offline mode.

**If the filesystem inbox is sufficient** (which is likely — the beats write
YAML files to inbox directories as part of their dir-transport, and NATS is
the inter-beat bus, not the on-disk format): document this explicitly and close.

**Suggestion:** A middle path worth considering is a `nats-to-dir` sidecar or
a one-shot `trim sync --from nats --to <dir>` subcommand that materializes the
current JetStream state to the filesystem inbox format. This keeps trim itself
transport-agnostic (it always reads files) while still working against the
JetStream-backed rig. The sidecar is trivial — it's a JetStream consumer that
writes each message as a YAML file, which is exactly what the dir-transport
publisher already does.

#### Cross-domain gate: remaining test coverage

The cross-domain evidence gate (`0c089e0`) has 4 new ACE cases. Six untested
edges are worth pinning (none blocking, all good hardening). Roll these into J2:

| # | Edge | Test to add |
|---|---|---|
| 1 | Downstream topology match (only Upstream tested) | Table case in [gate_test.go](file:///Users/ian/projects/go/thump/internal/clank/gate_test.go): SAO with a Downstream node matching the EvidenceRef.Subject → `anyCoherentLive` returns true |
| 2 | Two cross-domain refs, both out-of-topology | Table case: two Live EvidenceRefs, both with Subject not in SAO Topology → `anyCoherentLive` returns false |
| 3 | Non-nil SAO with empty topology | Table case: SAO with `Topology: &Topology{}` (non-nil but empty Upstream/Downstream) → every tagged ref fails, correct but untested |
| 4 | Subject string normalization | Document as convention (exact match, no case-folding); add a comment to `inTopology` at [gate.go:79-91](file:///Users/ian/projects/go/thump/internal/clank/gate.go#L79-L91) |
| 5 | `MetricsTool.Run` stamping Subject E2E | Integration test: `LoadEvidenceQueries` → `Run` → assert `EvidenceRef.Subject` is populated from the query's `subject` field |
| 6 | Gate reason priority (evidence AND dedupe both fail) | Table case: both `dedupeOK=false` and `evidenceOK=false` → assert the gate reason is the one that wins by switch order (evidence) |

**Done when:** every gap the live session surfaces is covered by a test, and
`trim incidents` works against whatever transport the rig actually uses.

---

### Wave J3 — Calibration read-path + vestigial floor cleanup 🎯

**Blast radius: `internal/hiss/policy.go`, `config/hiss/policy.yaml`,
`internal/hiss/policy_completeness_test.go`. Depends on J1 + real outcome data
(5 samples minimum).**

#### Part A: calibrate the floors

Today the confidence floors are static YAML — fixture-parity values that have never
been calibrated against real `agent_proposal_success_total` data:

| Class | Floor | Status |
|---|---|---|
| `service_failure` | 0.75 | Fixture parity, never exercised live |
| `redundancy_degraded` | 0.30 | Seed value, lowered to unblock first live run |

The calibration question: *at what confidence threshold does the model's success
rate justify auto-approval?* The data to answer it is
`agent_proposal_success_total{confidence_bucket, success}` — the counter
[metrics.go:47](file:///Users/ian/projects/go/thump/internal/clank/metrics.go#L47)
already emits. But it needs **5 samples minimum** per class to be meaningful,
and right now the sample count is zero.

**Steps:**

1. **Run the live tests (§2).** Collect outcome data across at least 5 reason
   runs (product-catalog × 2, cart × 1, hold-rebalance × 1, accelerate-recovery
   after approval × 1). This is the minimum sample count to move a floor.

2. **Query the calibration counter.** After a session:
   ```promql
   agent_proposal_success_total{confidence_bucket="0.7-0.8"}
   agent_proposal_success_total{confidence_bucket="0.8-0.9"}
   ```
   If the success rate at bucket 0.8-0.9 is ≥80%, the floor can safely be set at
   0.8 for that class. If it's 50%, the floor should stay at 0.75 or lower.

3. **Update `policy.yaml` with evidence.** Each floor adjustment gets a dated
   comment citing the sample count and success rate that justified it. The
   existing comments in
   [policy.yaml](file:///Users/ian/projects/go/thump/config/hiss/policy.yaml)
   already follow this discipline ("fixture-derived parity value, not yet
   calibrated…") — the calibrated version replaces these with the real numbers.

   Example:
   ```yaml
   service_failure: 0.80 # calibrated 2026-07-XX — 5 samples at bucket 0.8-0.9,
   # 4/5 ResultSuccess (80% success rate); raised from 0.75 fixture parity.
   ```

> [!IMPORTANT]
> **Do not automate floor adjustment.** The floor is a governance parameter (I-3:
> policy lives only in Governance). A script that reads a metric and rewrites
> `policy.yaml` is an agent writing its own permission slip. The operator reads
> the metric, makes a judgment call, and edits the YAML. The human is the loop.

#### Part B: remove vestigial floors

`dependency_saturation` and `resource_exhaustion` have no actuatable actions
after the dead-knob demote (`0fff1b2`). Their floors in
[policy.yaml:8-9](file:///Users/ian/projects/go/thump/config/hiss/policy.yaml#L8-L9) are
dead config — the
[TestPolicy_FloorsCoverEveryActuatableClass](file:///Users/ian/projects/go/thump/internal/hiss/policy_completeness_test.go#L22)
test keys off `actuate.BoundRefs`, so an unreferenced floor is invisible to CI.

**Steps:**

1. **Delete the `dependency_saturation: 0.75` and `resource_exhaustion: 0.75`
   lines** from `policy.yaml`. They serve no runtime purpose — hiss's
   `Authority.Evaluate` looks up `Floors[tier][class]` and a missing entry
   results in a zero floor (i.e., any nonzero confidence passes), which is the
   same as having no actuatable action at all.

2. **Verify `task ci` still passes.** The completeness test won't notice because
   it iterates `actuate.BoundRefs()`, and neither `dependency_saturation` nor
   `resource_exhaustion` has a bound ref.

3. **Add a comment** at the top of the `floors:` section documenting the removal
   and why:
   ```yaml
   floors:
     tier-1:
       # dependency_saturation and resource_exhaustion floors REMOVED 2026-07-XX —
       # no actuatable action references either class after the dead-knob demote
       # (0fff1b2). A floor for a class with no bound action is dead config.
       service_failure: 0.75
       redundancy_degraded: 0.3
   ```

#### Tests

**Test: `TestPolicy_VestigialFloorsAreNotShipped`** (optional, defensive)

If you want CI to catch a future re-addition of a floor for a class that has no
bound action:

```go
// TestPolicy_VestigialFloorsAreNotShipped pins that the shipped policy
// doesn't carry confidence floors for failure classes with no actuatable
// action — dead config that misleads operators into thinking a class is
// live.
func TestPolicy_VestigialFloorsAreNotShipped(t *testing.T) {
    t.Parallel()
    pol := loadShippedPolicy(t)
    boundClasses := make(map[proposal.FailureClass]bool)
    cat := loadShippedCatalog(t)
    for _, ref := range actuate.BoundRefs() {
        c, ok := cat.ByName(ref)
        if !ok { continue }
        for _, class := range c.ApplicableFailureClasses {
            boundClasses[class] = true
        }
    }
    for tier, classes := range pol.Floors {
        for class := range classes {
            if !boundClasses[class] {
                t.Errorf("policy.yaml has a floor for %s/%s, but no actuatable action references that class — vestigial config", tier, class)
            }
        }
    }
}
```

**Done when:** (a) at least one floor in `policy.yaml` is set from real
calibration data with a dated, evidence-backed comment — not a fixture-parity
guess; (b) the vestigial `dependency_saturation` and `resource_exhaustion`
floors are removed.

---

## §4 — Definition of done for Phase II

The operator can see, steer, and learn from what the machine does, when:

1. A `disable-product-catalog-failure` run converges to **`success`** with
   `reversed=false` and an AE datum near 0 — Phase I's missing screenshot (O-1).
2. `trim incidents` shows real incidents from a live cluster with correct stages,
   severities, and held durations (O-5).
3. `trim approve` resumes a held `accelerate-recovery` action through hiss's
   `approveHandler`, and thump executes it (O-6).
4. The confidence float is on the `reasoned` slog line and in the S3 transcript's
   checkpointed `ToolCalls`, with tests pinning both claims (J1).
5. At least one floor in `policy.yaml` is set from real calibration data, not
   a fixture-parity guess (J3).
6. The vestigial `dependency_saturation` and `resource_exhaustion` floors are
   removed from `policy.yaml` (J3).
7. `task ci` green throughout.

---

## §5 — Links

- Phase I guide (the predecessor — extends, doesn't supersede)
- [thump-charter.md](file:///Users/ian/projects/go/thump) — I-3 (policy in Governance), I-4 (catalog = autonomy boundary), I-8 (Learn = return edge), I-15 (operator surface)
- [transport.go:129 approveHandler](file:///Users/ian/projects/go/thump/internal/hiss/transport.go#L129) — the approval consumption path
- [pending.go](file:///Users/ian/projects/go/thump/internal/hiss/pending.go) — PendingHolds store
- [metrics.go](file:///Users/ian/projects/go/thump/internal/clank/metrics.go) — calibration metrics (confidence, calibration, effectiveness)
- [gate.go:67-73 anyCoherentLive](file:///Users/ian/projects/go/thump/internal/clank/gate.go#L67-L73) — cross-domain evidence gate
- [evidence-queries.yaml](file:///Users/ian/projects/go/thump/config/thump-test/whir/evidence-queries.yaml) — subject annotations, `rate([2m])` convergence queries
- [authored.go](file:///Users/ian/projects/go/thump/internal/contract/authored.go) — the 5-action catalog
- [engine.go:155-157](file:///Users/ian/projects/go/thump/internal/clank/engine.go#L155-L157) — the checkpoint call (pre-terminal)
- [engine.go:174-181](file:///Users/ian/projects/go/thump/internal/clank/engine.go#L174-L181) — the terminal `propose` tool call dispatch
- [store.go:25-29](file:///Users/ian/projects/go/thump/internal/clank/store.go#L25-L29) — the Store interface (`Checkpoint`, `Pending`, `Finish`)
- [store.go:241-258](file:///Users/ian/projects/go/thump/internal/clank/store.go#L241-L258) — Message, Completion, ToolCall types (the gap: Message doesn't carry ToolCalls)
- [proposal.go:107-116](file:///Users/ian/projects/go/thump/api/v1/proposal/proposal.go#L107-L116) — Candidate struct with `Confidence float64`
- [trim.go](file:///Users/ian/projects/go/thump/internal/trim/trim.go) — trim CLI entry point (Main, runApprove, runForce)
- [trim/transport.go](file:///Users/ian/projects/go/thump/internal/trim/transport.go) — filesystem-only Transport (Tick, Snapshot)
- [policy.yaml](file:///Users/ian/projects/go/thump/config/hiss/policy.yaml) — confidence floors (the calibration target)
- [policy_completeness_test.go](file:///Users/ian/projects/go/thump/internal/hiss/policy_completeness_test.go) — the floor coverage guard
- Running notes (2026-07-20 part 2) — the cross-domain fix
- Running notes (2026-07-20 part 1) — the live session that surfaced Bugs 1-3
- Running notes (2026-07-18 part 8) — the confidence persistence gap identification
