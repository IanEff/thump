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
		"Gate rejects a live citation whose subject sits outside the signal's topology": {
			ps:   psWithCrossDomainLiveEvidence(),
			want: verdict{Passed: false, Reason: "evidence"},
		},
		"Gate admits a live citation whose subject appears in the signal's topology": {
			ps:   psWithInTopologyLiveEvidence(),
			want: verdict{Passed: true, Reason: ""},
		},
		"Gate admits a cross-domain citation corroborated by an in-topology live ref": {
			ps:   psWithCrossDomainCorroboratedByInTopology(),
			want: verdict{Passed: true, Reason: ""},
		},
		"Gate rejects a subject-tagged live citation when no SAO was ever assembled": {
			ps:   psWithSubjectTaggedLiveEvidenceNoSAO(),
			want: verdict{Passed: false, Reason: "evidence"},
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

// argocdSAO is the frozen SAO an argocd-origin signal would actually carry:
// one-hop topology (cilium, rook-operator) — see testdata/detections/
// argocd-sync-burn.yaml — with product-catalog nowhere in it. This is the
// Bug 3 shape: a signal from one domain, an evidence citation from another,
// no declared edge between them.
func argocdSAO() *proposal.SAO {
	return &proposal.SAO{
		Version: 1,
		Topology: proposal.TopologySnapshot{
			Upstream: []proposal.NodeState{
				{Name: "cilium", State: "healthy"},
				{Name: "rook-operator", State: "healthy"},
			},
		},
	}
}

// psWithCrossDomainLiveEvidence reproduces the live-run bug: the sole live
// citation names a Subject (the OTel demo's product-catalog) that the
// argocd signal's own topology never declared a relationship to.
func psWithCrossDomainLiveEvidence() proposal.Set {
	return proposal.Set{
		Name:        "cross_domain",
		SAOSnapshot: argocdSAO(),
		Evidence:    []proposal.EvidenceRef{{Live: true, Subject: "product-catalog"}},
	}
}

// psWithInTopologyLiveEvidence is the same shape but the Subject names a
// node the SAO's topology actually lists — a legitimate live citation.
func psWithInTopologyLiveEvidence() proposal.Set {
	return proposal.Set{
		Name:        "in_topology",
		SAOSnapshot: argocdSAO(),
		Evidence:    []proposal.EvidenceRef{{Live: true, Subject: "rook-operator"}},
	}
}

// psWithCrossDomainCorroboratedByInTopology is the noisy-neighbor path this
// defence must keep open: a cross-domain citation alongside an independent
// in-topology one. The in-topology ref is what actually grounds the gate;
// the cross-domain ref rides along without vetoing it.
func psWithCrossDomainCorroboratedByInTopology() proposal.Set {
	return proposal.Set{
		Name:        "corroborated_cross_domain",
		SAOSnapshot: argocdSAO(),
		Evidence: []proposal.EvidenceRef{
			{Live: true, Subject: "product-catalog"},
			{Live: true, Subject: "rook-operator"},
		},
	}
}

// psWithSubjectTaggedLiveEvidenceNoSAO pins the fail-closed case: a Subject
// claim can't be confirmed against topology that was never assembled, so it
// can't ground the gate either — a nil SAOSnapshot must not be read as
// "topology doesn't apply, let it through".
func psWithSubjectTaggedLiveEvidenceNoSAO() proposal.Set {
	return proposal.Set{
		Name:     "subject_tagged_no_sao",
		Evidence: []proposal.EvidenceRef{{Live: true, Subject: "rook-operator"}},
	}
}
