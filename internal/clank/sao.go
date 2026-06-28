package clank

import "time"

type SAO struct {
	Version     int
	AssembledAt time.Time
	Signal      SignalSnapshot
	Topology    TopologySnapshot
	Change      ChangeSnapshot
}

type SignalSnapshot struct {
	Confidence  float64
	Metric      string
	Severity    Severity
	BlastRadius BlastRadius
}

type TopologySnapshot struct {
	Upstream   []NodeState
	Downstream []NodeState
}

type NodeState struct {
	Name          string
	State         string
	DegradedSince time.Duration
	TrafficShare  float64
}

type ChangeSnapshot struct {
	Events []ChangeEvent
}

type ChangeEvent struct {
	ID     string
	Type   string // deploy | config | flag | rollback
	Target string
	Age    time.Duration

	CausalLikelihood    float64       // 0 until scored
	Rationale           []string      // human-legible evidence per score
	PredictedSignals    []string      // indicators expected if this change is causal (negative-signal check, defence 3)
	HistoricalStaleness time.Duration // topology age of the case-base match (freshness-decay, defence 2)
}

type BlastRadius struct {
	AffectedPct         float64
	Velocity            string
	DownstreamConsumers int
}

type Severity struct {
	DegradationPct float64
	Trajectory     string
}
