package thump_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/thump"
)

func richSet() proposal.Set {
	return proposal.Set{
		Recommended: "p1",
		RankingRationale: &proposal.RankingRationale{
			DominantAxis:   "time_to_effect",
			VelocityWeight: "accelerating",
		},
		Hypotheses: []proposal.Hypothesis{
			{Name: "dependency_saturation", Weight: 0.7},
			{Name: "traffic_shift", Weight: 0.3},
		},
		Proposals: []proposal.Candidate{
			{
				ID: "p1", ContractRef: "throttle-non-critical-paths",
				Confidence: 0.62, ComputedConfidence: 0.81, ConfidenceCeilingBound: true,
				Rank: 1, Citations: []string{"burn", "topology"},
			},
			{
				ID: "p2", ContractRef: "restart-rgw-pool",
				Confidence: 0.40, Rank: 2,
			},
		},
		Evidence: []proposal.EvidenceRef{
			{Tool: "metrics", Query: "burn", Summary: "rgw pool saturating"},
			{Tool: "kube", Query: "topology", Summary: "rgw pods degraded"},
		},
	}
}

func TestRenderNotes_RendersEveryRankedCandidateAndWhyTheWinnerWon(t *testing.T) {
	t.Parallel()

	got := thump.RenderNotesForTest(richSet())

	want := "Recommended: p1\n" +
		"Ranked by: time_to_effect (velocity: accelerating)\n\n" +
		"Hypotheses:\n" +
		"  - dependency_saturation (weight 0.70)\n" +
		"  - traffic_shift (weight 0.30)\n\n" +
		"Candidates:\n" +
		"* #1 throttle-non-critical-paths    confidence=0.62 (ceiling-bound, computed=0.81) citations=[burn, topology]\n" +
		"  #2 restart-rgw-pool               confidence=0.40\n\n" +
		"Evidence:\n" +
		"  - [metrics] burn: rgw pool saturating\n" +
		"  - [kube] topology: rgw pods degraded\n"

	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong rendered notes for a richly populated Set", diff)
	}
}

func TestRenderNotes_OmitsEachSectionTheSetLeftUnauthored(t *testing.T) {
	t.Parallel()
	bareSet := proposal.Set{
		Recommended: "p1",
		Proposals:   []proposal.Candidate{{ID: "p1", ContractRef: "c1", Rank: 1}},
	}
	cases := map[string]string{
		"renderNotes omits the ranking-rationale line when the Set carries none": "Ranked by:",
		"renderNotes omits the hypotheses block when the Set names none":         "Hypotheses:",
		"renderNotes omits the evidence block when the Set cites none":           "Evidence:",
		"renderNotes omits citations for a candidate that names none":            "citations=[",
	}
	for name, missing := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := thump.RenderNotesForTest(bareSet)
			if strings.Contains(got, missing) {
				t.Errorf("expected %q not to appear in an unauthored section, got:\n%s", missing, got)
			}
		})
	}
}
