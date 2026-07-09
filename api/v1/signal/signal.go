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
	Name          string // rattle's label for the event, e.g. "checkout-latency-burn-accel-001" — descriptive only, never the dedup key
	Fingerprint   string // rattle's dedup key, assigned once per event — clank never recomputes it; threaded through as proposal.Set.SignalRef to suppress a new proposal for the same open event
	OriginService string // the affected service — the reason loop's subject
	ServiceTier   string // gates which ActionContract catalog entries apply (Catalog.ApplicableToTier)
	DetectorType  string // which rattle detector fired, e.g. "burn_rate_acceleration" — carried through for the audit trail; clank does not branch on it
	Divergence    Divergence
	Topology      TopologyContext
	Traffic       TrafficContext
	Impact        Impact
	ContractRef   string    // the ActionContract the SLO's author associated with this signal at declaration time — informational; the engine validates each Candidate's own ContractRef against the catalog, not this one
	DetectedAt    time.Time // when rattle observed the divergence — clank's freshness math runs off ChangeEvent.HistoricalStaleness, not this field
}

// Divergence is rattle's read of how far the observed metric has drifted from
// baseline — the "is this real?" signal-strength half of the two-confidence
// split (see § The clank ⟷ rattle boundary in CLAUDE.md). clank reads
// Confidence, never sets it, and never conflates it with a Candidate's own
// hypothesis confidence.
type Divergence struct {
	Metric     string  // the metric name being watched, e.g. "latency_p99"
	Observed   float64 // the current reading
	Baseline   float64 // the expected reading it's diverging from
	Confidence float64 // rattle's signal-strength confidence ("is this real?") — never the model's hypothesis confidence ("how sure of this fix?")
	Trajectory string  // e.g. "accelerating" — rattle's own read of the drift's direction
}

// Impact is rattle's two-axis read of how bad this detection is: Severity
// (how bad — a metric property) and BlastRadius (how broadly exposed — a
// who/what property). The ranker reads both independently; it never
// collapses them into one "badness" number.
type Impact struct {
	Severity    Severity
	BlastRadius BlastRadius
}

// Severity is the metric-degradation axis of Impact — how far off the SLO
// objective the affected object has drifted. It says nothing about who else
// is exposed; that's BlastRadius.
type Severity struct {
	DegradationPct float64 // 0.0-1.0 fraction of the SLO's error budget consumed, clamped at 1.0
	Trajectory     string  // e.g. "accelerating" — rattle's own read of the drift's direction
}

// BlastRadius is the exposure axis of Impact — how broadly the detection
// reaches, not how bad it is at the source (that's Severity). Its Velocity
// is what the ranker reads to decide whether time-to-effect becomes the
// dominant ranking axis (Ranker.Rank).
type BlastRadius struct {
	AffectedPct         float64 // 0.0-1.0 fraction of traffic or consumers affected, clamped at 1.0
	Velocity            string  // e.g. "accelerating", "fast" — read by Ranker.Rank
	DownstreamConsumers int     // count of distinct downstream services in the blast radius
}

// TopologyContext is rattle's own read of the dependency graph immediately
// around the detection — clank's intake falls back to it only when its own
// TopologySource has nothing yet (see proposal.TopologySnapshot, the SAO's copy).
type TopologyContext struct {
	Upstream   []ObservedNode
	Downstream []ObservedNode
}

// ObservedNode is one service rattle observed upstream or downstream of the
// detection — a name plus a coarse health read, not a full topology record.
type ObservedNode struct {
	Service string
	State   string // "healthy" | "degraded" | "down" by rattle's convention, not a compiler-enforced enum — flows unchanged into proposal.NodeState.State, where the causal scorer's negative-signal check tests for exactly "degraded"
}

// TrafficContext is rattle's own traffic-share reading for the affected
// object — a narrower, separate feed from Impact.BlastRadius; clank's intake
// does not read it yet.
type TrafficContext struct {
	AffectedPct    float64 // 0.0-1.0 fraction of traffic affected
	Baseline       float64 // the pre-incident traffic level it's compared against
	BaselineWindow string  // the window the baseline was computed over, e.g. "7d"
}
