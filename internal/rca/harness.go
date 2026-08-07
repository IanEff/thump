package rca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace/noop"
	runtime "k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/reason"
)

// noopTopology and noopChange leave the graded suite's change source
// unwired. With no change ever resolving, LikelihoodOK is never true, so
// the Causal weight can never fire — a sweep over it measures a flat
// surface against this harness, whatever it does against a live rig.
type noopTopology struct{}

func (noopTopology) Topology(context.Context, signal.Detection) (proposal.TopologySnapshot, error) {
	return proposal.TopologySnapshot{}, nil
}

type noopChange struct{}

func (noopChange) Changes(context.Context, signal.Detection) (proposal.ChangeSnapshot, error) {
	return proposal.ChangeSnapshot{}, nil
}

// harness is one built engine plus the teardown for the fake backends behind
// it. Close must run even when RunCase errors, or a graded suite leaks one
// httptest server per row.
type harness struct {
	engine    *clank.Engine
	detection signal.Detection
	close     func()
}

// newHarness builds the graded engine: the shipped catalog and failure
// classes, a fake Prometheus scripted from the row's evidence, and a kube
// fake — never the production wiring. profile selects which config/<rig>
// evidence-queries.yaml to grade against, and kubeObjects seeds the kube
// fake — this package holds no rig- or Ceph-specific knowledge itself, both
// come from the caller.
func newHarness(c Case, model reason.Model, w clank.ScoringWeights, transcripts, profile string, kubeObjects ...runtime.Object) (harness, error) {
	det, err := loadDetection(c.Fixture)
	if err != nil {
		return harness{}, err
	}

	cat, err := contract.LoadCatalogFile(configPath("actions", "catalog.yaml"), contract.Preconditions)
	if err != nil {
		return harness{}, fmt.Errorf("load catalog: %w", err)
	}
	classes, err := contract.LoadFailureClassesFile(configPath("actions", "failure-classes.yaml"))
	if err != nil {
		return harness{}, fmt.Errorf("load failure classes: %w", err)
	}
	ev, err := evidence.LoadEvidenceConfig(configPath(profile, "whir", "evidence-queries.yaml"))
	if err != nil {
		return harness{}, fmt.Errorf("load evidence queries: %w", err)
	}

	prom := fakePrometheus(promQLByName(ev.Queries), c.Evidence)

	tools := map[string]reason.Tool{
		// A second live backend is what makes GroundingMany reachable at
		// all — with only one distinct reason.Tool, coherentLiveCitations
		// can never count past one and the top tier is structurally dead.
		"metrics": &evidence.MetricsTool{BaseURL: prom.URL, Queries: ev.Queries},
		"kube": &evidence.KubeTool{
			Client:   kubefake.NewSimpleClientset(kubeObjects...),
			Subjects: ev.Index,
		},
	}

	cases := clank.NewCaseBase()
	limits := clank.DefaultLimits()

	eng := &clank.Engine{
		Intake:         clank.NewIntake(noopTopology{}, noopChange{}),
		Model:          model,
		Tools:          tools,
		Catalog:        cat,
		FailureClasses: classes,
		Ranker:         clank.NewRanker(),
		Gate:           clank.ReadinessGate{},
		Store:          clank.NewDirStore(transcripts),
		Scorer:         &clank.CausalScorerImpl{Prior: cases},
		Prior:          cases,
		DedupeWindow:   time.Hour,
		Ledger:         clank.NewMemProposalLog(),
		Pub: &publish.DirPublisher[proposal.Set]{
			Dir:  transcripts,
			Name: func(s proposal.Set) string { return "set-" + s.SignalRef + ".yaml" },
		},
		MaxSteps: limits.MaxSteps,
		Weights:  w,
		Tracer:   noop.Tracer{},
	}

	return harness{engine: eng, detection: det, close: prom.Close}, nil
}

// RunCase grades one scenario against model and returns its Row. It never
// panics on a declined set — an "insufficient" row has no candidate at all,
// so every candidate read is guarded. The emitted set is also written to
// transcripts as <signalRef>.set.json beside the conversation's .jsonl,
// because replay needs Evidence[].Live and .Subject to score a grounding
// tier and neither field survives in the conversation transcript alone.
func RunCase(ctx context.Context, c Case, model reason.Model, w clank.ScoringWeights, transcripts, profile string, kubeObjects ...runtime.Object) (Row, error) {
	h, err := newHarness(c, model, w, transcripts, profile, kubeObjects...)
	if err != nil {
		return Row{}, err
	}
	defer h.close()

	set, err := h.engine.Propose(ctx, h.detection)
	if err != nil {
		return Row{}, fmt.Errorf("propose %s: %w", c.Name, err)
	}
	if err := writeSet(transcripts, set); err != nil {
		return Row{}, fmt.Errorf("write set for %s: %w", c.Name, err)
	}
	return grade(c, set), nil
}

// writeSet marshals set to <transcripts>/<signalRef>.set.json. SignalRef
// substitutes for a filesystem-safe name; it is a rattle Kind():Object pair
// and can carry slashes.
func writeSet(transcripts string, set proposal.Set) error {
	raw, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	name := strings.ReplaceAll(set.SignalRef, "/", "_") + ".set.json"
	return os.WriteFile(filepath.Join(transcripts, name), raw, 0o600) //nolint:gosec // G703: transcripts comes from a CLI flag, env var, or os.TempDir — operator-supplied, not user input
}
