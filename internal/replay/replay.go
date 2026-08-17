package replay

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/unseal"
)

// Main replays one recorded transcript through a real clank.Engine and
// prints the proposal.Set it emits under the given weights — the same
// re-scoring tune sweeps in aggregate, provable here against one named
// fixture instead of only the grid's mean confidence.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonlPath := fs.String("jsonl", "", "path to the recorded transcript (required)")
	setPath := fs.String("set", "", "path to the transcript's paired .set.json (required)")
	weightsFile := fs.String("weights-file", "", "YAML ScoringWeights to replay under (default: clank.DefaultScoringWeights)")
	asJSON := fs.Bool("json", false, "print the replayed proposal.Set as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *jsonlPath == "" || *setPath == "" {
		_, _ = fmt.Fprintln(stderr, "usage: replay --jsonl <path> --set <path> [--weights-file path] [--json]")
		return 2
	}

	tr, err := LoadTranscript(*jsonlPath, *setPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "replay:", err)
		return 1
	}

	weights := clank.DefaultScoringWeights()
	if *weightsFile != "" {
		weights, err = clank.LoadWeightsFile(*weightsFile)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "replay:", err)
			return 1
		}
	}

	set, err := Propose(context.Background(), tr, weights)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "replay:", err)
		return 1
	}

	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(set); err != nil {
			_, _ = fmt.Fprintln(stderr, "replay:", err)
			return 1
		}
		return 0
	}

	line, err := json.Marshal(set)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "replay:", err)
		return 1
	}
	_, _ = fmt.Fprint(stdout, unseal.Summarize(line))
	return 0
}

// Propose re-runs tr through a real clank.Engine under w. Everything the
// model decided is replayed; everything scoreConfidences computes is
// recomputed — which is exactly the property a sweep needs, since the
// weights apply after the model and cannot change what the run cited.
func Propose(ctx context.Context, tr Transcript, w clank.ScoringWeights) (proposal.Set, error) {
	cat, err := contract.LoadCatalogFile(configPath("thump-test", "actions", "catalog.yaml"), contract.Preconditions)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load catalog: %w", err)
	}
	classes, err := contract.LoadFailureClassesFile(configPath("thump-test", "actions", "failure-classes.yaml"))
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load failure classes: %w", err)
	}

	cases := clank.NewCaseBase()
	eng := &clank.Engine{
		Intake:         clank.NewIntake(replayTopology{tr.Set}, replayChange{tr.Set}),
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

// replayChange hands back the change events the recorded run reasoned
// over — the causal scorer's Likelihood otherwise recomputes at zero on
// every replay, since InTopology can never be true against events the run
// never saw.
type replayChange struct {
	set proposal.Set
}

func (r replayChange) Changes(context.Context, signal.Detection) (proposal.ChangeSnapshot, error) {
	if r.set.SAOSnapshot == nil {
		return proposal.ChangeSnapshot{}, nil
	}
	return r.set.SAOSnapshot.Change, nil
}

// detectionFrom rebuilds the Detection the run started from. The SAO
// snapshot froze it on the emitted set precisely so the audit trail could be
// replayed against what the loop actually saw.
func detectionFrom(set proposal.Set) signal.Detection {
	d := signal.Detection{Fingerprint: set.SignalRef, ServiceTier: set.ServiceTier, SLORef: set.SLORef}
	if set.SAOSnapshot != nil {
		d.OriginService = set.SAOSnapshot.Signal.OriginService
		d.Divergence.Confidence = set.SAOSnapshot.Signal.Confidence
	}

	return d
}
