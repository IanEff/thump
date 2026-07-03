package clank_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
)

func TestGate(t *testing.T) {
	t.Parallel()

	// verdict is the slice of the GateResult this suite asserts on: did the set
	// pass, and — when it didn't — which minimum vetoed it. Comparing the pair
	// (not just Passed) is what keeps wantWhy honest.
	type verdict struct {
		Passed bool
		Reason string
	}

	cases := map[string]struct {
		ps        clank.ProposalSet
		openDupes []clank.ProposalSet
		want      verdict
	}{
		"Gate rejects a set citing no live evidence": {
			ps:   psWithNoEvidence(),
			want: verdict{Passed: false, Reason: "evidence"},
		},
		"Gate rejects a historical-only set with no live citation": {
			ps:   psHistoricalOnly(),
			want: verdict{Passed: false, Reason: "evidence"},
		},
		"Gate suppresses a set with an open duplicate": {
			ps:        psWithLiveEvidence(),
			openDupes: []clank.ProposalSet{{}},
			want:      verdict{Passed: false, Reason: "dedupe"},
		},
		"Gate admits a set with live evidence and no dupe": {
			ps:   psWithLiveEvidence(),
			want: verdict{Passed: true, Reason: ""},
		},
	}

	var gate clank.ReadinessGate
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := gate.Evaluate(tc.ps, tc.openDupes)
			got := verdict{Passed: res.Passed, Reason: res.Reason}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong gate verdict (-want +got)\n", diff)
			}
		})
	}
}

func psWithLiveEvidence() clank.ProposalSet {
	return clank.ProposalSet{Name: "live_evidence", Evidence: []clank.EvidenceRef{{Live: true}}}
}

func psHistoricalOnly() clank.ProposalSet {
	return clank.ProposalSet{Name: "historical_evidence", Evidence: []clank.EvidenceRef{{Live: false}}}
}

func psWithNoEvidence() clank.ProposalSet {
	return clank.ProposalSet{Name: "no_evidence"}
}
