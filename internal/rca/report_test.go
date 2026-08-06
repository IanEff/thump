package rca_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rca"
)

// TestReport_DerivesTheFloorFromTheTableRatherThanAnAuthoredConstant pins the
// bar to the population. The floor was a const whose comment did the
// subtraction in prose, and it went stale the first time a row stopped being a
// known miss.
func TestReport_DerivesTheFloorFromTheTableRatherThanAnAuthoredConstant(t *testing.T) {
	t.Parallel()

	want := 0
	for _, c := range rca.Table() {
		if !c.KnownMiss {
			want++
		}
	}
	if diff := cmp.Diff(want, rca.Floor()); diff != "" {
		t.Error("floor disagrees with the table it is derived from", diff)
	}
}

// TestReport_CountsOnlyPassingRowsTowardTheFloor keeps the tally honest: a
// known miss that happens to pass still counts, because the floor is a
// population bar and not a per-row exemption.
func TestReport_CountsOnlyPassingRowsTowardTheFloor(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		rows []rca.Row
		want int
	}{
		"NewReport scores zero when every row missed": {
			rows: []rca.Row{{Pass: false}, {Pass: false}},
			want: 0,
		},
		"NewReport counts a passing known miss toward the score": {
			rows: []rca.Row{{Pass: true, KnownMiss: true}, {Pass: false}},
			want: 1,
		},
		"NewReport counts every passing row": {
			rows: []rca.Row{{Pass: true}, {Pass: true}, {Pass: false}},
			want: 2,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, rca.NewReport(tc.rows).Scored); diff != "" {
				t.Error("wrong score", diff)
			}
		})
	}
}
