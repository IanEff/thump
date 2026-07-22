# Phase K Live Rig Test Plan — Grounding & Verification on `thump-test`

**Written 2026-07-22.** This test plan defines the live validation suite for Phase K (`docs/phase-k-plan.md`) against the multi-domain `thump-test` live rig (`~/projects/ceph/thump-test`). 

The primary objective of Phase K live testing is to empirically verify that **belief is strictly bound to evidence** across the entire pipeline (rattle → clank → hiss → thump → click), eliminating the structural failures identified in the Phase J post-mortem (Findings 9, 10, and 11).

---

## 1. Test Rig Architecture & Configuration

### 1.1 Dual-Domain Topology (`thump-test`)
The test rig host is `thump-test` (replacing `rook-gce-k3s`), running two orthogonal application domains on a single Kubernetes cluster:
1. **Ceph Storage Domain**: Rook-Ceph OSDs, Mons, Mgr, RGW, and S3 traffic generator.
2. **OTel Astronomy Shop Domain**: Microservices application with `flagd` feature flags, OpenTelemetry collector, and Locust load generator.

Because the two domains share no telemetry signals, failure classes, or action contracts, `thump-test` serves as the acid test for cross-domain isolation.

### 1.2 Required Pre-Flight Rig Arming
Per `.claude/skills/chaos-testing/SKILL.md`, configure the live execution values in `deploy/tilt-values-thump-test.yaml`:

```yaml
thump:
  executor: live
killSwitch:
  armed: true
beats:
  clank:
    dedupeWindow: 10m  # Shortened from 1h default to prevent stuck open fingerprints during 10m chaos windows
```

> **CRITICAL**: After modifying values, trigger **both** services in Tilt so Deployments update:
> ```bash
> tilt trigger thump
> tilt trigger clank
> ```
> Verify live Deployment environment before proceeding:
> ```bash
> kubectl get deployment clank -n thump -o jsonpath='{.spec.template.spec.containers[0].env}'
> ```

---

## 2. Phase K Invariant Verification Matrix

| Finding / Build | Symptom / Invariant Under Test | Test Scenario | Target Metric / Evidence | Expected Verdict / Result |
|---|---|---|---|---|
| **Finding 10** (K1 + K2) | **Cross-domain action execution** (e.g. ArgoCD signal triggering cart action). | **Scenario 1**: Cross-Domain Filler Injection | `product_catalog_error_ratio` cited for `argocd` signal | Gate fails with `reason="evidence"`. Candidate **not** delivered. |
| **Finding 9** (K4 + K5) | **Log-hunting budget death** (burning turns looking for log lines). | **Scenario 2**: Metric-Only Corroboration | Live Prometheus metric without log lines | Model proposes immediately on turn 1-2. Zero log searches. |
| **Finding 11** (K3) | **Constant confidence `0.95`** (no variance, ungrounded self-report). | **Scenario 3**: Grounding-Based Confidence | 0, 1, and 2+ live in-topology citations | 0 refs: ~0.27 (hiss floor veto)<br>1 ref: ~0.63<br>2+ refs: ~0.90 |
| **Autonomous Win** (Phase I/K) | **Hold-rebalance race condition** (mark-out vs pipeline latency). | **Scenario 4**: OSD Autonomous Hold-Rebalance | `osd-pod-failure-autonomous.yaml` + `mon_osd_down_out_interval=300s` | `noout` applied at ~150s (before 300s mark-out). Ceph stays HEALTH_WARN, zero rebalance thrash. |
| **Multi-Domain** | **Interference across orthogonal domains**. | **Scenario 5**: Concurrent Dual Chaos | Ceph OSD failure + OTel Demo Flag failure | Dual independent detections; zero cross-domain citation or misrouting. |

---

## 3. Detailed Test Scenarios

### Scenario 1: Cross-Domain Isolation & Ungrounded Citation Rejection (Finding 10 Fix)
* **Goal**: Prove that K1 schema validation + K2 citation-grounded readiness gate prevents out-of-domain actions even when in-topology filler evidence exists in the run context.
* **Fault Injection**:
  1. Trigger OTel demo `productCatalogFailure` flag via script or ConfigMap patch:
     ```bash
     chaos/flag-product-catalog.sh starve
     ```
  2. Synthesize or route an out-of-domain signal (e.g., Ceph or ArgoCD alert) while `product-catalog` metrics are degraded.
* **Execution & Verification**:
  1. Clank executes reasoning loop.
  2. Inspect transcript in `transcripts/<fingerprint>/<RunID>/*.json`.
  3. Verify model recommendation for out-of-domain action specifies `citations: ["product_catalog_error_ratio"]`.
  4. Verify `clank.ReadinessGate.Evaluate` checks the recommendation's citations against the signal's SAO topology snapshot (`rook-operator` / `argocd`).
* **Expected Outcome**:
  - `Gate.Passed` is `false`.
  - `Gate.Reason` is `"evidence"`.
  - Hiss receives a gated/declined set and delivers `no_action`.
  - Action is **never** executed against the target cluster.

---

### Scenario 2: Metric-Only Corroboration & Log-Hunt Elimination (Finding 9 & K4/K5 Fix)
* **Goal**: Verify that updated `seedPrompt` (K4) and flag-state evidence query (K5) allow Clank to converge on metric evidence without wasting turns searching for log lines.
* **Fault Injection**:
  1. Apply RGW client delay (slow requests, high latency, zero HTTP error logs):
     ```bash
     kubectl apply -f chaos/rgw-client-delay.yaml
     ```
  2. Alternative: Trigger `recommendationCacheFailure` flagd fault.
* **Execution & Verification**:
  1. Monitor Clank transcript and slog output:
     ```bash
     until kubectl logs -n thump -l app.kubernetes.io/component=clank --since=5m | grep -q '"reasoned"'; do sleep 5; done
     ```
  2. Count loop turns in `transcripts/<fingerprint>/<RunID>/*.json`.
* **Expected Outcome**:
  - Turn count to proposal $\le 2$.
  - Model cites `rgw_client_latency` or `flag_state` metric.
  - Zero tool calls to `logs` searching for text confirmation.
  - `Status.Phase` reaches `success`.

---

### Scenario 3: Grounding-Based Confidence Variance (Finding 11 & K3 Fix)
* **Goal**: Prove candidate confidence is computed as `min(computed, selfReported)` and varies deterministically based on live in-topology citation count.
* **Fault Injection**:
  1. Execute `chaos/pg-num-starve.sh starve` (moves `ceph-rgw-saturation` raw latency).
* **Test Cases**:
  - **Case 3A (0 Live Citations)**: Model proposes action citing 0 live refs.
    - *Computed*: $0.90 \times 0.3 = 0.27$.
    - *Outcome*: Hiss floor veto (`0.27 < 0.75`).
  - **Case 3B (1 Live Citation)**: Model cites 1 live in-topology ref (`ceph_rgw_get_lat`).
    - *Computed*: $0.90 \times 0.7 = 0.63$.
    - *Outcome*: Floored by Hiss (`0.63 < 0.75`).
  - **Case 3C (2+ Live Citations)**: Model cites 2 live in-topology refs (`ceph_rgw_get_lat` AND `ceph_rgw_put_lat`).
    - *Computed*: $0.90 \times 1.0 = 0.90$.
    - *Outcome*: Passes Hiss floor (`0.90 \ge 0.75`) and executes.
* **Expected Outcome**:
  - Confidence log lines show dynamic values ($0.27, 0.63, 0.90$) instead of static $0.95$.

---

### Scenario 4: OSD Autonomous Hold-Rebalance (`osd-pod-failure-autonomous.yaml`)
* **Goal**: Prove autonomous pipeline speed ($\sim 150\text{s}$) lands `hold-rebalance` (`ceph osd set noout`) inside the 300s window before mark-out.
* **Pre-requisite Setup**:
  1. Set cluster mark-out interval to 300s in Rook-Ceph cluster settings:
     ```bash
     kubectl exec -n rook-ceph deploy/rook-ceph-tools -- ceph config set global mon_osd_down_out_interval 300
     ```
* **Fault Injection**:
  1. Apply autonomous OSD pod failure (480s duration):
     ```bash
     kubectl apply -f chaos/osd-pod-failure-autonomous.yaml
     ```
* **Execution & Verification**:
  1. Background listener waits for Hiss decision:
     ```bash
     until kubectl logs -n thump -l app.kubernetes.io/component=hiss --since=10m | grep -q '"decision"'; do sleep 5; done
     ```
  2. Verify Ceph `noout` flag set:
     ```bash
     kubectl exec -n rook-ceph deploy/rook-ceph-tools -- ceph osd dump | grep flags
     ```
* **Expected Outcome**:
  - Detection fires at $T+30\text{s}$.
  - Proposal delivered and Hiss approves `hold-rebalance` at $T+150\text{s}$ ($< 300\text{s}$).
  - `noout` flag applied. OSD remains `down, in`. PGs stay `degraded`, zero rebalance data movement.
  - At $T+480\text{s}$, Chaos Mesh clears fault; OSD recovers to `up, in`.
  - Reversal watcher clears `noout` flag on convergence.

---

### Scenario 5: Dual-Domain Concurrent Chaos
* **Goal**: Verify non-interference when Ceph storage and OTel demo experience simultaneous faults.
* **Fault Injection**:
  1. Apply `osd-pod-failure-autonomous.yaml` AND `chaos/flag-product-catalog.sh starve` within 30s of each other.
* **Execution & Verification**:
  1. Monitor Rattle detection stream for two distinct fingerprints.
  2. Trace Clank transcripts for both runs.
* **Expected Outcome**:
  - Two independent proposal sets generated (`ps-ceph-...` and `ps-otel-...`).
  - Zero cross-citation between Ceph metrics and OTel demo metrics.
  - Hiss approves actions for both domains independently.
  - Both remediations succeed and auto-revert upon fault restoration.

---

## 4. Execution Discipline & Safety Rules

1. **Non-Blocking Waiting**: Never use arbitrary `sleep 600` commands. Use event-driven background loops:
   ```bash
   until kubectl logs -n thump -l app.kubernetes.io/component=hiss --since=5m | grep -q '"decision"'; do sleep 5; done
   ```
2. **Log Grep Precision**: Match exact `slog` message strings (`"detection"`, `"reasoned"`, `"decision"`).
3. **Clean-Up Routine**: Always run teardown scripts after each scenario:
   ```bash
   kubectl delete -f chaos/osd-pod-failure-autonomous.yaml || true
   kubectl delete -f chaos/rgw-client-delay.yaml || true
   chaos/pg-num-starve.sh restore || true
   chaos/flag-product-catalog.sh restore || true
   ```
4. **Transcript Archival**: After test completion, record transcript S3/GCS paths into `docs/phase-k-live-session-notes.md`.
