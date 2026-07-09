package hiss_test

import (
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
)

func TestAuditable(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		d       decision.Decision
		wantErr bool
	}{
		"Auditable accepts a fully stamped approval": {
			d: goldenDecision(), wantErr: false,
		},
		"Auditable rejects a decision with no policy version": {
			d: withoutPolicyVersion(goldenDecision()), wantErr: true,
		},
		"Auditable rejects a decision with a zero evaluation time": {
			d: withoutEvaluatedAt(goldenDecision()), wantErr: true,
		},
		"Auditable rejects an escalation that gives no reason": {
			d: reasonlessEscalation(goldenDecision()), wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tc.d.Auditable()
			if tc.wantErr && err == nil {
				t.Error("an unautidable decision must not pass Auditable")
			}
			if !tc.wantErr && err != nil {
				t.Error("a fully stamped decision must be autidable: ", err)
			}
		})
	}
}

func goldenDecision() decision.Decision {
	return decision.Decision{
		ID:            "dec:slo_burn:ceph-rgw:1000",
		ProposalRef:   "ps-ceph-rgw-001",   // ps.Name
		SignalRef:     "slo_burn:ceph-rgw", // ps.SignalRef
		CandidateRef:  "p1",
		Verdict:       decision.VerdictApproved,
		Reasons:       nil,
		RequestedBand: decision.BandObserve,
		GrantedBand:   decision.BandObserve,
		FloorApplied:  0.75,
		PolicyVersion: "v1",
		EvaluatedAt:   frozenNow(),
	}
}

func frozenNow() time.Time {
	return time.Unix(1000, 0)
}

func withoutPolicyVersion(d decision.Decision) decision.Decision {
	d.PolicyVersion = ""
	return d
}

func withoutEvaluatedAt(d decision.Decision) decision.Decision {
	d.EvaluatedAt = time.Time{}
	return d
}

func reasonlessEscalation(d decision.Decision) decision.Decision {
	d.Verdict, d.Reasons, d.GrantedBand = decision.VerdictEscalate, nil, ""
	return d
}
