package clank_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/clank"
)

func TestGate(t *testing.T) {
	t.Parallel()
	withLive := psWithLiveEvidence()
	histOnly := psHistoricalOnly()
	noEvidence := psWithNoEvidence()

	cases := []struct {
		name      string
		ps        clank.ProposalSet
		openDupes []clank.ProposalSet
		wantPass  bool
		wantWhy   string
	}{
		{"rejects when no evidence", noEvidence, nil, false, "evidence"},
		{"rejects historical-only with no live citation", histOnly, nil, false, "evidence"},
		{"suppresses an open duplicate", withLive, []clank.ProposalSet{{}}, false, "dedupe"},
		{"admits live evidence + no dupe", withLive, nil, true, ""},
	}
	var gate clank.ReadinessGate
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := gate.Evaluate(tc.ps, tc.openDupes, testPolicy())
			if got.Passed != tc.wantPass {
				t.Errorf("gate verdict incorrect for %q\n%s", tc.name, cmp.Diff(tc.wantPass, got.Passed))
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

func testPolicy() clank.GatePolicy {
	return clank.GatePolicy{}
}
