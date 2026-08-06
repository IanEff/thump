package rca

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace/noop"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
// fake — never the production wiring.
func newHarness(c Case, model reason.Model, w clank.ScoringWeights, transcripts string) (harness, error) {
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
	ev, err := evidence.LoadEvidenceConfig(configPath("thump-test", "whir", "evidence-queries.yaml"))
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
			Client: kubefake.NewSimpleClientset(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rook-ceph-mon-a",
					Namespace: "rook-ceph",
					Labels:    map[string]string{"app": "rook-ceph-mon"},
				},
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			}),
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
// so every candidate read is guarded.
func RunCase(ctx context.Context, c Case, model reason.Model, w clank.ScoringWeights, transcripts string) (Row, error) {
	h, err := newHarness(c, model, w, transcripts)
	if err != nil {
		return Row{}, err
	}
	defer h.close()

	set, err := h.engine.Propose(ctx, h.detection)
	if err != nil {
		return Row{}, fmt.Errorf("propose %s: %w", c.Name, err)
	}
	return grade(c, set), nil
}
