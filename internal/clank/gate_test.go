package clank_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
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
		ps        proposal.Set
		openDupes []proposal.Set
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
			openDupes: []proposal.Set{{}},
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

func psWithLiveEvidence() proposal.Set {
	return proposal.Set{Name: "live_evidence", Evidence: []proposal.EvidenceRef{{Live: true}}}
}

func psHistoricalOnly() proposal.Set {
	return proposal.Set{Name: "historical_evidence", Evidence: []proposal.EvidenceRef{{Live: false}}}
}

func psWithNoEvidence() proposal.Set {
	return proposal.Set{Name: "no_evidence"}
}
