package clank_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/clank"
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
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Hypotheses:   []clank.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}

	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: clank.TopologySnapshot{
				Downstream: []clank.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: clank.ChangeSnapshot{Events: []clank.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model: model,
		Tools: map[string]clank.Tool{"metrics": metricsTool{}},
		Catalog: clank.NewStaticCatalog([]clank.ActionContract{{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []clank.FailureClass{clank.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
		}}),
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		Ledger:       clank.NewMemProposalLog(),
		Sink:         &clank.DirSink{Dir: outbox},
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
