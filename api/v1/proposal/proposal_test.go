package proposal_test

import (
	"testing"

	"github.com/ianeff/thump/api/v1/proposal"
)

// TestSet_ConfidenceForReturnsTheMatchingCandidateConfidence pins that
// ConfidenceFor returns the named candidate's float, not 0 — the claim
// that the confidence is recoverable from the Set (I-11).
func TestSet_ConfidenceForReturnsTheMatchingCandidateConfidence(t *testing.T) {
	t.Parallel()
	s := proposal.Set{
		Proposals:   []proposal.Candidate{{ID: "c1", Confidence: 0.85}, {ID: "c2", Confidence: 0.72}},
		Recommended: "c1",
	}
	if got := s.ConfidenceFor("c1"); got != 0.85 {
		t.Errorf("ConfidenceFor(c1): want 0.85, got %v", got)
	}
	if got := s.ConfidenceFor("missing"); got != 0 {
		t.Errorf("ConfidenceFor(missing): want 0, got %v", got)
	}
}
