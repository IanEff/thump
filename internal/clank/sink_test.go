package clank_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/clank"
)

// func ExampleMarkdownSink_Deliver() {
// 	sink := &clank.MarkdownSink{W: os.Stdout}
// 	_ = sink.Deliver(context.Background(), clank.ProposalSet{
// 		FailureClass: clank.ClassDependencySaturation,
// 		Recommended:  "prop-001",
// 		Proposals:    []clank.Candidate{{ID: "prop-001", ContractRef: "throttle-non-critical-paths", Rank: 1}},
// 	})
// 	// Output:
// 	// ## ProposalSet: dependency_saturation (1 considered)
// 	// **Recommended:** prop-001 — throttle-non-critical-paths
// }

func TestDirSink_WritesOneFilePerProposalSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink := &clank.DirSink{Dir: dir}

	if err := sink.Deliver(context.Background(), clank.ProposalSet{
		Name: "n", SignalRef: "slo_burn:ceph-rgw",
	}); err != nil {
		t.Fatal(err)
	}
	// named by fingerprint so a re-proposal of the same incident overwrites,
	// never piles up — the file inbox inherits the ledger's dedup intent.
	if _, err := os.Stat(filepath.Join(dir, "slo_burn:ceph-rgw.yaml")); err != nil {
		t.Errorf("DirSink must write <fingerprint>.yaml: %v", err)
	}
}

func ExampleYAMLSink_Deliver() {
	sink := &clank.YAMLSink{W: os.Stdout}
	// An Example can't t.Fatal; surfacing the error to stdout makes a delivery
	// failure show up as a diff against the // Output: block rather than vanishing.
	if err := sink.Deliver(context.Background(), clank.ProposalSet{
		FailureClass: clank.ClassDependencySaturation,
		Recommended:  "prop-001",
		Proposals: []clank.Candidate{
			{ID: "prop-001", ContractRef: "throttle-non-critical-paths", Rank: 1},
		},
	}); err != nil {
		fmt.Println("deliver error:", err)
	}
	// Output:
	// failureClass: dependency_saturation
	// proposals:
	// - contractRef: throttle-non-critical-paths
	//   id: prop-001
	//   rank: 1
	// recommended: prop-001
}
