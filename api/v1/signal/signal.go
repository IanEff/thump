// Package signal holds the rattle⟷clank contract surface: the Detection
// that rattle (the Signal Plane) emits and clank (the Reasoning Plane) consumes
// read-only, plus the shared value objects (Severity, BlastRadius) that ride the
// boundary in both directions. It is a leaf package — it imports only the stdlib,
// and clank depends on it, never the reverse.
//
// v1 is additive-only: never rename, retype, or repurpose a field here, since
// other processes (not just other packages) depend on this exact shape.
package signal

import "time"

// Detection is rattle's detected reliability event — clank's sole input. (Within
// rattle's own vocabulary this is the SignalDetection; clank reproduces it here as
// signal.Detection.) clank trusts it: it never recomputes the Fingerprint (rattle's
// dedup key) nor re-judges signal trustworthiness. clank imports this type; never owns it.
type Detection struct {
	Name          string
	Fingerprint   string // DEDUPE KEY assigned by rattle
	OriginService string
	ServiceTier   string
	DetectorType  string
	Divergence    Divergence
	Topology      TopologyContext
	Traffic       TrafficContext
	Impact        Impact
	ContractRef   string
	DetectedAt    time.Time
}

type Divergence struct {
	Metric     string
	Observed   float64
	Baseline   float64
	Confidence float64
	Trajectory string
}

type Impact struct {
	Severity    Severity
	BlastRadius BlastRadius
}

type Severity struct {
	DegradationPct float64
	Trajectory     string
}

type BlastRadius struct {
	AffectedPct         float64
	Velocity            string
	DownstreamConsumers int
}

type TopologyContext struct {
	Upstream   []ObservedNode
	Downstream []ObservedNode
}

type ObservedNode struct {
	Service string
	State   string // healthy | degrade | down - via rattle
}

type TrafficContext struct {
	AffectedPct    float64
	Baseline       float64
	BaselineWindow string
}
