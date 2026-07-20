package approval_test

import (
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/approval"
)

func TestApproval_Auditable(t *testing.T) {
	t.Parallel()
	now := time.Now()

	tests := map[string]struct {
		a       approval.Approval
		wantErr bool
	}{
		"a fully populated Approval is Auditable": {
			a: approval.Approval{SignalRef: "fp-1", Approver: "alice", ApprovedAt: now},
		},
		"an Approval missing SignalRef is not Auditable": {
			a:       approval.Approval{Approver: "alice", ApprovedAt: now},
			wantErr: true,
		},
		"an Approval missing Approver is not Auditable": {
			a:       approval.Approval{SignalRef: "fp-1", ApprovedAt: now},
			wantErr: true,
		},
		"an Approval missing ApprovedAt is not Auditable": {
			a:       approval.Approval{SignalRef: "fp-1", Approver: "alice"},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tc.a.Auditable()
			if tc.wantErr && err == nil {
				t.Fatal("want an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatal("want no error, got:", err)
			}
		})
	}
}
