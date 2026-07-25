# thump — architecture

> ⚠️ **Provisional.** Ported from the author's working design docs and lightly
> de-jargoned; still written for someone already holding the context, not yet
> rewritten for outside readers. Accurate, but terse and assumption-heavy in
> places. A public-facing rewrite is owed. Start with `README.md` if this is your
> first pass.
>
> Where this and the author's working notes disagree, the working notes are
> newer — see `README.md` § Source of truth.

Sourced from *Agentic Reliability Engineering* (the four-plane architecture,
agent-driven incident response, agentic delivery pipelines, and the
belief-formation defenses), with build method from *The Power of Go: Tools* and
*Tests*, and delivery conventions from *Shipping Go*.

Companion doc: `invariants.md` — the fifteen rules this shape exists to make
enforceable, plus the smell test a design review runs.

---

## The shape: five beats, four planes

**Planes, not layers.** They're orthogonal concerns with their own failure
containment, intersecting only through boundary objects. Each plane has a single
job and a **never**-clause — and the never-clauses are the architecture. Remove
them and you have an ordinary automation pipeline.

| Beat | Plane | Single job | Never |
|------|-------|-----------|-------|
| **rattle** (Detect) | Signal | Represent reality | **Never interpret** — facts only |
| **clank** (Reason) | Reasoning | Structured truth → candidate actions | **Never act** — proposals only |
| **hiss** (Govern) | Governance | Permission evaluation | **Never re-reason** — verdicts only |
| **thump** (Act) | Execution | Approved actions → outcomes | **Never decide** — contracts only |
| **click** (Learn) | — *(return edge)* | Outcomes feed the next cycle | **Never a module** |

Planes are conceptual separations, not necessarily separate services. Today it's
one binary per beat in one Go module, which is fine *as long as* the four
responsibilities stay structurally distinguishable. The moment they mash
together, the safety properties stop holding — and they stop holding quietly,
which is the dangerous part.

**Why click isn't a sixth box.** A "Learn service" has to reach across every
plane boundary to do its job, and reassembles the monolith you just decomposed.
click is thump's `Outcome` flowing back into clank's case base and calibration
numbers: wiring, not a binary. It lives in `internal/clank/click.go`
(`Click.Absorb`, `ReturnEdge`) and `metrics.go`'s `Recorder`.

**The anti-pattern this engine exists not to be** is the Signal/Execution
collapse: monitoring triggering remediation scripts directly, with nothing
reasoning in between and nothing governing the result.

---

## The seams: producer-owned boundary objects

The boundary objects are the real design surface — the planes are whatever code
sits on either side of them. **One producer per object**; consumers read it and
never reach into the producer's internals.

`api/v1/signal` is the model to copy: a leaf package importing only `time`,
and the sole thing rattle and clank share.

| Boundary object | Our type | Producer → Consumer |
|---|---|---|
| Signal Contract | `signal.Detection` + `SignalContract` | rattle → clank |
| Candidate Action | `proposal.Set` | clank → hiss |
| Governance verdict | `decision.Decision` (approved / hold / escalate / rejected) | hiss → thump |
| Action Contract | `contract.ActionContract` catalog | **humans author** → clank proposes from, thump executes from |
| Outcome Signals | `outcome.Outcome` | thump → click's return edge |
| Approval | `approval.Approval` on `thump.approvals` | operator → hiss re-issues |

Non-approvals are an audit record, never silence. A rejected or held proposal
still lands in the trail with its reason attached.

**The audit thread.** One identifier runs the whole way through:
`Detection.Fingerprint` → `Set.SignalRef` → `Decision.SignalRef` →
`Outcome.SignalRef`. That thread is the entire audit trail — nothing in this
system needs a second source of truth to answer "why did it do that."

---

## Two observability layers, never fused

- **The audit trail** answers *why?* — the assembled evidence snapshot, the whole
  ranked proposal set, evidence refs, rationales, the governance verdict and the
  policy version it was reached under.
- **Telemetry** answers *how fast?* — structured `slog`, rate/error/duration
  metrics, and spans at each seam.

Keep them separate. Fusing them gets you dashboards that can't answer either
question: a trace is not a rationale, and a rationale is not a latency budget.

---

## Deployment shape

The beats deploy as operators. Their outputs ride a stream (NATS JetStream) and
land in an S3-offloaded write-ahead log — **the log is the system of record.**
etcd holds slow, human-authored config only, and as little of that as possible;
there is no custom resource per noun. Any reporting view is a read-model,
rebuildable from the log, never a second source of record.

Delivery is **at-least-once**, and identity is the producer-assigned
fingerprint. Every consumer is idempotent and dedupes on that fingerprint —
never on transport metadata like a filename or a sequence number. "Exactly-once"
is at-least-once composed with idempotent consumers; this project implements the
composition and declines the marketing term.

---

## What is deliberately not built

Named so it isn't mistaken for an oversight:

- **A risk shaper beyond reversibility and blast tier** — the computed risk band
  is deliberately narrow.
- **Signal validity checks in clank** — that's rattle's job, by construction.
- **Declarative preconditions.** The registry that binds a precondition name in
  YAML to a Go predicate exists and is tested; no shipped action declares one,
  so no expression language was built. It gets one the day a real domain needs
  it, not before.
- **Dynamic baselines.** rattle uses a flat trailing mean ± Kσ window.
- **Parallel decision-making.** One signal, one loop, one verdict.
