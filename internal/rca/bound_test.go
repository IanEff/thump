package rca_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rca"
)

// TestBoundBy_SeparatesAnEvidenceBoundDrawFromAModelHedgingBelowTheFloor pins
// the distinction this phase exists to measure: hiss governs on
// min(computed, selfReported) (internal/clank/confidence.go:80), so a draw can
// miss the 0.75 floor for two unrelated reasons — thin corroboration or a
// cautious model — and AT's sweep folded both into one underFloor count, which
// is why its acme row read 30/30 PASS while 18 of those draws would have
// escalated live.
func TestBoundBy_SeparatesAnEvidenceBoundDrawFromAModelHedgingBelowTheFloor(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		row  rca.Row
		want rca.Bound
	}{
		"one citation grounds at 0.7 and no model number can rescue it": {
			row:  rca.Row{Computed: 0.70, Confidence: 0.70},
			want: rca.BoundEvidence,
		},
		"evidence grounds fully but the model hedges under the floor": {
			row:  rca.Row{Computed: 1.00, Confidence: 0.65},
			want: rca.BoundSelfReport,
		},
		"both clear the floor, so this draw would have auto-approved": {
			row:  rca.Row{Computed: 1.00, Confidence: 0.90},
			want: rca.BoundNone,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := rca.BoundBy(tc.row, 0.75)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("BoundBy mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
