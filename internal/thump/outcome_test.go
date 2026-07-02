package thump_test

import (
	"testing"

	"github.com/ianeff/clank/internal/thump"
)

func TestOutcomeAuditable(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		o       thump.Outcome
		wantErr bool
	}{
		"Auditable accepts a fully stamped dry-run outcome": {
			o: goldenOutcome(), wantErr: false,
		},
		"Auditable rejects an outcome with no decision ref": {
			o: withoutDecisionRef(goldenOutcome()), wantErr: true,
		},
		"Auditable rejects an outcome with a zero execution time": {
			o: withoutExecutedAt(goldenOutcome()), wantErr: true,
		},
		"Auditable rejects an outcome with no mode": {
			o: withoutMode(goldenOutcome()), wantErr: true,
		},
		"Auditable rejects a failure that gives no error text": {
			o: silentFailure(goldenOutcome()), wantErr: true,
		},
		// the defence-4 row, both directions: the vocabulary ACCEPTS a
		// half-worked-and-not-settling outcome when it explains itself…
		"Auditable accepts a partial non-converging outcome that explains itself": {
			o: explainedPartial(goldenOutcome()), wantErr: false,
		},
		// …and rejects the silent version. Representable ≠ excusable.
		"Auditable rejects a partial non-converging outcome with no error text": {
			o: silentPartial(goldenOutcome()), wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tc.o.Auditable()
			if tc.wantErr && err == nil {
				t.Error("an unauditable outcome must not pass Auditable")
			}
			if !tc.wantErr && err != nil {
				t.Error("a fully stamped outcome must be auditable:", err)
			}
		})
	}
}
