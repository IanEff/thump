package replay

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
)

// Propose re-runs tr through a real clank.Engine under w. Everything the
// model decided is replayed; everything scoreConfidences computes is
// recomputed — which is exactly the property a sweep needs, since the
// weights apply after the model and cannot change what the run cited.
func Propose(ctx context.Context, tr Transcript, w clank.ScoringWeights) (proposal.Set, error) {
	cat, err := contract.LoadCatalogFile(configPath("actions", "catalog.yaml"), contract.Preconditions)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load catalog: %w", err)
	}
	classes, err := contract.LoadFailureClassesFile(configPath("actions", "failure-classes.yaml"))
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load failure classes: %w", err)
	}

	cases := clank.NewCaseBase()
	eng := &clank.Engine{
		Intake:         clank.NewIntake(replayTopology{tr.Set}, replayChange{}),
		Model:          NewModel(tr.Completions),
		Tools:          BuildTools(tr.Set),
		Catalog:        cat,
		FailureClasses: classes,
		Ranker:         clank.NewRanker(),
		Gate:           clank.ReadinessGate{},
		Store:          clank.NewMemStore(),
		Scorer:         &clank.CausalScorerImpl{Prior: cases},
		Prior:          cases,
		DedupeWindow:   time.Hour,
		Ledger:         clank.NewMemProposalLog(),
		MaxSteps:       clank.DefaultLimits().MaxSteps,
		Weights:        w,
		Tracer:         noop.Tracer{},
	}

	return eng.Propose(ctx, detectionFrom(tr.Set))
}

// replayTopology hands back the topology the recorded run reasoned over.
// coherentSubject walks it to decide whether an EvidenceRef's Subject is
// in-topology, so replaying without it fails every subject closed and drops
// every candidate to the ungrounded tier.
type replayTopology struct {
	set proposal.Set
}

func (r replayTopology) Topology(context.Context, signal.Detection) (proposal.TopologySnapshot, error) {
	if r.set.SAOSnapshot == nil {
		return proposal.TopologySnapshot{}, nil
	}
	return r.set.SAOSnapshot.Topology, nil
}

type replayChange struct{}

func (replayChange) Changes(context.Context, signal.Detection) (proposal.ChangeSnapshot, error) {
	return proposal.ChangeSnapshot{}, nil
}

// detectionFrom rebuilds the Detection the run started from. The SAO
// snapshot froze it on the emitted set precisely so the audit trail could be
// replayed against what the loop actually saw.
func detectionFrom(set proposal.Set) signal.Detection {
	d := signal.Detection{Fingerprint: set.SignalRef, ServiceTier: set.ServiceTier}
	if set.SAOSnapshot != nil {
		d.OriginService = set.SAOSnapshot.Signal.OriginService
		d.Divergence.Confidence = set.SAOSnapshot.Signal.Confidence
	}

	return d
}
