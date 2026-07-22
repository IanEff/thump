# C4 Architecture Model: `thump` Agentic SRE Engine

> **System Overview**: `thump` is a general-purpose, DRAL-based (Detect, Reason, Authorize/Govern, Act, Learn) agentic SRE system designed for Kubernetes clusters (testing against Rook/Ceph). It implements a 5-beat pipeline (`rattle` → `clank` → `hiss` → `thump` → `click`) governed by strict safety invariants (C4 / Autonomy boundaries, hard kill switch, multi-source evidence floors, and reversible contracts).

---

## Level 1: System Context Diagram

![Level 1 System Context Diagram](c4-system-context.svg)

```mermaid
flowchart TB
    subgraph Users["Users & Governance"]
        Operator["SRE / Cluster Operator"]
    end

    subgraph CoreSystem["thump System (DRAL Engine)"]
        Engine["thump Agentic Reliability Engine"]
    end

    subgraph Environment["Infrastructure & Telemetry"]
        Prometheus["Prometheus / Telemetry"]
        Cluster["Kubernetes / Ceph Cluster"]
        WAL["S3 / WAL Storage"]
    end

    Prometheus -- "Sends alerts & metrics" --> Engine
    Operator -- "Configures policy & arms killswitch" --> Engine
    Engine -- "Executes / reverses actions" --> Cluster
    Engine -- "Queries live telemetry" --> Prometheus
    Engine -- "Persists decision stream" --> WAL
```

---

## Level 2: Container Diagram (The 5 Beats & Infrastructure)

![Level 2 Container Diagram](c4-containers.svg)

```mermaid
flowchart LR
    Prom["Prometheus Telemetry"]

    subgraph Pipeline["thump 5-Beat Pipeline"]
        Rattle["1. rattle (Signal)"]
        Clank["2. clank (Reasoning)"]
        Hiss["3. hiss (Governance)"]
        ThumpExec["4. thump (Execution)"]
        Click["5. click (Learning)"]

        Ledger[("WAL / Proposal Ledger")]
        CaseBase[("CaseBase Store")]
    end

    K8s["Kubernetes / Ceph Cluster"]
    KillSwitch[["THUMP_KILLSWITCH"]]

    Prom --> Rattle
    Rattle -- "SignalDetection" --> Clank
    Prom -- "MetricsTool queries" --> Clank
    Clank --> Ledger
    Clank -- "Passed ProposalSet" --> Hiss
    Hiss -- "Governed Decision" --> ThumpExec
    KillSwitch -. "Safety Gate" .-> ThumpExec
    ThumpExec -- "Catalog Actions" --> K8s
    ThumpExec -- "Outcome" --> Click
    Click --> CaseBase
    CaseBase --> Clank
```

---

## Level 3: Component Diagram (Golden Path Focus: `clank`, `hiss`, & `thump`)

![Level 3 Component Diagram](c4-components.svg)

```mermaid
flowchart TB
    subgraph Clank["clank (Reasoning Engine)"]
        Intake["SAO Intake"]
        Engine["Engine Loop"]
        MetricsTool["MetricsTool"]
        Catalog["StaticCatalog"]
        Gate["ReadinessGate"]
    end

    subgraph Hiss["hiss (Governance)"]
        Authority["Authority Evaluator"]
        Shaper["RiskBandShaper"]
    end

    subgraph ThumpExec["thump (Execution)"]
        Actuator["Actuator"]
        Runner["DryRun / LiveRun"]
    end

    Intake --> Engine
    Engine --> MetricsTool
    Engine --> Catalog
    Engine --> Gate
    Gate -- "Delivers ProposalSet" --> Authority
    Authority --> Shaper
    Shaper -- "Emits Approved Verdict" --> Actuator
    Actuator --> Runner
```

---

## End-to-End Golden Path Sequence (`node-death` Scenario)

![Golden Path Sequence Diagram](c4-sequence.svg)

```mermaid
sequenceDiagram
    autonumber
    participant R as rattle (Signal)
    participant C as clank (Reasoning)
    participant G as ReadinessGate
    participant H as hiss (Governance)
    participant T as thump (Execution)
    participant L as click (Learning)

    R->>C: SignalDetection (Fingerprint: slo_burn:ceph-rgw)
    Note over C: Intake builds SAO snapshot
    C->>C: ToolCall metrics("osds_down") => Ref 1
    C->>C: ToolCall metrics("pgs_backfilling") => Ref 2
    C->>C: Propose candidate "hold-rebalance" (Conf: 0.9, Blast: med)
    C->>G: Evaluate Readiness Gate
    Note over G: Check: Budget AND Dedupe AND Evidence (at least 2 Live Refs)
    G-->>C: Gate.Passed = true
    C->>H: Deliver ProposalSet
    Note over H: Evaluate Policy (floor 0.75 is below 0.9 conf, MaxBand act_reversible)
    H-->>T: Decision (Verdict: Approved, GrantedBand: act_reversible)
    Note over T: Check THUMP_KILLSWITCH == armed (or DryRun)
    T->>T: Render Execution Order & Execute
    T-->>L: Emit Outcome (Result: Rendered / Settle: Converged)
    L->>C: Absorb Outcome into CaseBase
```

---

## Declarative Architecture in D2 Format

![Declarative Architecture in D2 Format](c4-d2.svg)

For developers using D2 (`d2 docs/architecture.d2 docs/c4-d2.svg`), here is the equivalent D2 source code:

```d2
# thump System C4 Architecture in D2
direction: right

classes: {
  beat: {
    style: {
      fill: "#e1f5fe"
      stroke: "#0288d1"
      stroke-width: 2
      border-radius: 6
    }
  }
  safety: {
    style: {
      fill: "#ffebee"
      stroke: "#d32f2f"
      stroke-dash: 3
    }
  }
}

prometheus: Prometheus Telemetry {
  shape: cylinder
}

thump_system: thump Agentic SRE {
  rattle: Beat 1: rattle (Signal) {
    class: beat
    description: "Detects reliability anomalies; emits fingerprinted SignalDetection."
  }

  clank: Beat 2: clank (Reasoning) {
    class: beat
    sao: SAO Intake
    gate: Readiness Gate (Budget & Dedupe & Evidence)
    catalog: Action Catalog Boundary
  }

  hiss: Beat 3: hiss (Governance) {
    class: beat
    policy: Policy Evaluator (Floors & Blast Ceilings)
  }

  thump_exec: Beat 4: thump (Execution) {
    class: beat
    actuator: Actuator & DryRun/LiveRun
    killswitch: THUMP_KILLSWITCH {
      class: safety
    }
  }

  click: Beat 5: click (Learning Edge) {
    class: beat
    casebase: CaseBase Engine
  }
}

cluster: Target Kubernetes / Ceph Cluster {
  shape: cloud
}

prometheus -> thump_system.rattle: "Ingests Alerts & Metrics"
thump_system.rattle -> thump_system.clank.sao: "emits SignalDetection"
prometheus -> thump_system.clank: "Live metrics queries (MetricsTool)"
thump_system.clank.gate -> thump_system.hiss.policy: "delivers ProposalSet (if Gate passes)"
thump_system.hiss.policy -> thump_system.thump_exec.actuator: "emits Governed Decision (Approved)"
thump_system.thump_exec.actuator -> cluster: "Executes catalog action (e.g. hold-rebalance)"
thump_system.thump_exec.actuator -> thump_system.click.casebase: "Outcome feedback edge"
```
