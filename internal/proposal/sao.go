package proposal

import (
	"time"

	"github.com/ianeff/clank/internal/signal"
)

type SAO struct {
	Version     int              `json:"version,omitempty" yaml:"version,omitempty"`
	AssembledAt time.Time        `json:"assembledAt,omitempty" yaml:"assembledAt,omitempty"`
	Signal      SignalSnapshot   `json:"signal,omitempty" yaml:"signal,omitempty"`
	Topology    TopologySnapshot `json:"topology,omitempty" yaml:"topology,omitempty"`
	Change      ChangeSnapshot   `json:"change,omitempty" yaml:"change,omitempty"`
}

// SignalSnapshot's Severity/BlastRadius fields are internal/signal types,
// which carry no json/yaml tags of their own (that package sits outside
// hiss/thump's boundary — see CLAUDE.md § rattle). Their fields round-trip
// today only because both writer and reader used to agree by accident on
// the same library; the tags below stop this struct's OWN fields from
// drifting, but Severity/BlastRadius are still a landmine if a second
// codec ever touches them. Flag for internal/signal if that day comes.
type SignalSnapshot struct {
	Confidence  float64            `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Metric      string             `json:"metric,omitempty" yaml:"metric,omitempty"`
	Severity    signal.Severity    `json:"severity,omitempty" yaml:"severity,omitempty"`
	BlastRadius signal.BlastRadius `json:"blastRadius,omitempty" yaml:"blastRadius,omitempty"`
}

type TopologySnapshot struct {
	Upstream   []NodeState `json:"upstream,omitempty" yaml:"upstream,omitempty"`
	Downstream []NodeState `json:"downstream,omitempty" yaml:"downstream,omitempty"`
}

type NodeState struct {
	Name          string        `json:"name,omitempty" yaml:"name,omitempty"`
	State         string        `json:"state,omitempty" yaml:"state,omitempty"`
	DegradedSince time.Duration `json:"degradedSince,omitempty" yaml:"degradedSince,omitempty"`
	TrafficShare  float64       `json:"trafficShare,omitempty" yaml:"trafficShare,omitempty"`
}

type ChangeSnapshot struct {
	Events []ChangeEvent `json:"events,omitempty" yaml:"events,omitempty"`
}

type ChangeEvent struct {
	ID     string        `json:"id,omitempty" yaml:"id,omitempty"`
	Type   string        `json:"type,omitempty" yaml:"type,omitempty"` // deploy | config | flag | rollback
	Target string        `json:"target,omitempty" yaml:"target,omitempty"`
	Age    time.Duration `json:"age,omitempty" yaml:"age,omitempty"`

	CausalLikelihood    float64       `json:"causalLikelihood,omitempty" yaml:"causalLikelihood,omitempty"`       // 0 until scored
	Rationale           []string      `json:"rationale,omitempty" yaml:"rationale,omitempty"`                     // human-legible evidence per score
	PredictedSignals    []string      `json:"predictedSignals,omitempty" yaml:"predictedSignals,omitempty"`       // indicators expected if this change is causal (negative-signal check, defence 3)
	HistoricalStaleness time.Duration `json:"historicalStaleness,omitempty" yaml:"historicalStaleness,omitempty"` // topology age of the case-base match (freshness-decay, defence 2)
}
