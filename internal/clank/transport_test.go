package clank_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
)

func TestTransport_ADetectionInTheInboxIsProposedAndArchived(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	outbox := t.TempDir()

	// a real seam Detection (reuse seamDetection from seam_test.go — same package_test)
	det := seamDetection(t) // Fingerprint "slo_burn:ceph-rgw", ServiceTier "tier-1"
	raw, err := yaml.Marshal(det)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "det.yaml"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	tr := &clank.Transport{
		Inbox:  inbox,
		Engine: newProposingEngine(t, outbox), // scripted fakeModel that calls propose; DirSink → outbox
	}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("a well-formed detection must tick cleanly:", err)
	}

	// the proposal landed in the outbox (hiss's inbox), and the input was archived:
	if got := yamlCount(t, outbox); got != 1 {
		t.Errorf("want one delivered ProposalSet in the outbox, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(inbox, "processed", "det.yaml")); err != nil {
		t.Errorf("a processed detection must be archived, not left in the inbox: %v", err)
	}
}

// newProposingEngine builds an Engine identical in shape to newTestEngine
// (engine_test.go) — same Intake fakes, same Tools, same Catalog — but wired
// with a real DirSink into outbox instead of an in-memory captureSink, and a
// fixed scripted model that always investigates then proposes. Its only job
// is proving Transport → Propose → delivered file, so the script never varies.
func newProposingEngine(t *testing.T, outbox string) *clank.Engine {
	t.Helper()
	model := &fakeModel{script: []clank.Completion{
		// turn 1: gather live evidence — required for the gate's evidence floor.
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		// turn 2: propose a catalogued action.
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}

	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: proposal.TopologySnapshot{
				Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model: model,
		Tools: map[string]clank.Tool{"metrics": metricsTool{}},
		Catalog: contract.NewStaticCatalog([]contract.ActionContract{{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
		}}),
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		Ledger:       clank.NewMemProposalLog(),
		Pub:          &publish.DirPublisher[proposal.Set]{Dir: outbox, Name: func(ps proposal.Set) string { return ps.SignalRef }},
		MaxSteps:     8,
	}
}

// yamlCount counts the *.yaml files directly inside dir — how the transport
// test proves "one ProposalSet landed in the outbox" without caring about
// its filename (DirSink names files by fingerprint, not by test-chosen name).
func yamlCount(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}

type erroringModel struct{ err error }

func (m erroringModel) Complete(context.Context, []clank.Message, []clank.ToolSpec) (clank.Completion, error) {
	return clank.Completion{}, m.err
}

func TestTransport_GivesUpAfterFiveFailures(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	det := seamDetection(t)
	raw, _ := yaml.Marshal(det)
	_ = os.WriteFile(filepath.Join(inbox, "det.yaml"), raw, 0o600)

	eng := newProposingEngine(t, t.TempDir()) // reuse the fixture from transport_test.go
	eng.Model = erroringModel{err: errors.New("boom: model unreachable")}

	tr := &clank.Transport{Inbox: inbox, Engine: eng}
	for i := 0; i < 5; i++ {
		if err := tr.Tick(context.Background()); err != nil {
			t.Fatalf("tick %d: transport-level error, want per-file retry: %v", i, err)
		}
	}

	if _, err := os.Stat(filepath.Join(inbox, "stalled", "det.yaml")); err != nil {
		t.Errorf("want det.yaml in stalled/ after 5 failures: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inbox, "det.yaml")); !os.IsNotExist(err) {
		t.Error("want det.yaml gone from the inbox root after stalling")
	}
}
