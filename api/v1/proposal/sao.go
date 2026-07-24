package proposal

import (
	"time"

	"github.com/ianeff/thump/api/v1/signal"
)

// SAO is the versioned snapshot clank's intake assembles before the reason
// loop starts — Signal + Topology + Change together, read-only, never
// re-derived mid-loop. The Version an emitted Set carries in its
// SAOSnapshot must be > 0: a zero Version means nothing was actually
// assembled, and the audit trail would be dangling, not just incomplete.
type SAO struct {
	Version     int              `json:"version,omitempty" yaml:"version,omitempty"`
	AssembledAt time.Time        `json:"assembledAt,omitempty" yaml:"assembledAt,omitempty"`
	Signal      SignalSnapshot   `json:"signal,omitempty" yaml:"signal,omitempty"`
	Topology    TopologySnapshot `json:"topology,omitempty" yaml:"topology,omitempty"`
	Change      ChangeSnapshot   `json:"change,omitempty" yaml:"change,omitempty"`
}

// SignalSnapshot is intake's copy of the originating signal.Detection's
// impact read — Confidence is rattle's signal-strength number
// (Divergence.Confidence), never a Candidate's hypothesis confidence.
//
// Severity and BlastRadius are signal.Severity/signal.BlastRadius values,
// which carry no json/yaml tags of their own (that package sits outside
// hiss/thump's wire boundary — see CLAUDE.md § rattle). They round-trip
// today only because writer and reader happen to agree on the same codec;
// the tags on this struct's own fields stop those from drifting, but
// Severity/BlastRadius stay a landmine if a second codec ever touches them.
type SignalSnapshot struct {
	Confidence    float64            `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Metric        string             `json:"metric,omitempty" yaml:"metric,omitempty"`
	Severity      signal.Severity    `json:"severity,omitempty" yaml:"severity,omitempty"`
	BlastRadius   signal.BlastRadius `json:"blastRadius,omitempty" yaml:"blastRadius,omitempty"`
	OriginService string             `json:"originService,omitempty" yaml:"originService,omitempty"` // the affected service
}

// TopologySnapshot is the dependency graph clank's intake read at assembly
// time — the causal scorer's Topological component and defence 3's
// negative-signal check both walk Upstream/Downstream to find whether an
// event's Target sits in-path and degraded.
type TopologySnapshot struct {
	Upstream   []NodeState `json:"upstream,omitempty" yaml:"upstream,omitempty"`
	Downstream []NodeState `json:"downstream,omitempty" yaml:"downstream,omitempty"`
}

// NodeState is one service's read at SAO assembly time — richer than
// signal.ObservedNode (DegradedSince, TrafficShare) because clank's own
// TopologySource can report more than rattle's inline TopologyContext carries.
type NodeState struct {
	Name          string        `json:"name,omitempty" yaml:"name,omitempty"`
	State         string        `json:"state,omitempty" yaml:"state,omitempty"` // the causal scorer's negative-signal and topological checks test for exactly "degraded"
	DegradedSince time.Duration `json:"degradedSince,omitempty" yaml:"degradedSince,omitempty"`
	TrafficShare  float64       `json:"trafficShare,omitempty" yaml:"trafficShare,omitempty"` // read directly as the topological score when this node is in-path and degraded
}

// ChangeSnapshot is every recent change event intake found for the
// originating signal — the causal scorer's raw material, one CausalScore
// computed per Events entry.
type ChangeSnapshot struct {
	Events []ChangeEvent `json:"events,omitempty" yaml:"events,omitempty"`
}

// ChangeEvent is one deploy/config/flag/rollback the change source reported —
// what the causal scorer weighs to decide whether it's the cause, not a
// confirmed cause on its own.
type ChangeEvent struct {
	ID     string        `json:"id,omitempty" yaml:"id,omitempty"`
	Type   string        `json:"type,omitempty" yaml:"type,omitempty"` // deploy | config | flag | rollback
	Target string        `json:"target,omitempty" yaml:"target,omitempty"`
	Age    time.Duration `json:"age,omitempty" yaml:"age,omitempty"` // feeds the scorer's temporal component, which decays with a 30-minute half-life

	CausalLikelihood    float64       `json:"causalLikelihood,omitempty" yaml:"causalLikelihood,omitempty"`       // mirrors CausalScore.Likelihood's shape for this event; the scorer returns a separate CausalScore rather than writing this field in v1, so it reads 0 until something copies the score back
	Rationale           []string      `json:"rationale,omitempty" yaml:"rationale,omitempty"`                     // mirrors CausalScore.Rationale's shape — same v1 caveat: nothing writes it yet
	PredictedSignals    []string      `json:"predictedSignals,omitempty" yaml:"predictedSignals,omitempty"`       // indicators expected in topology if this change is the cause — each one absent decrements Likelihood (negative-signal check, defence 3)
	HistoricalStaleness time.Duration `json:"historicalStaleness,omitempty" yaml:"historicalStaleness,omitempty"` // topology age of the matching case-base incident — the older this is, the more the historical score decays (freshness-decay, defence 2)
}
