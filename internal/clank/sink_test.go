package clank_test

import (
	"context"
	"os"

	"github.com/ianeff/clank/internal/clank"
)

func ExampleMarkdownSink_Deliver() {
	sink := &clank.MarkdownSink{W: os.Stdout}
	_ = sink.Deliver(context.Background(), clank.ProposalSet{
		FailureClass: clank.ClassDependencySaturation,
		Recommended:  "prop-001",
		Proposals:    []clank.Candidate{{ID: "prop-001", ContractRef: "throttle-non-critical-paths", Rank: 1}},
	})
	// Output:
	// ## ProposalSet: dependency_saturation (1 considered)
	// **Recommended:** prop-001 — throttle-non-critical-paths
}
