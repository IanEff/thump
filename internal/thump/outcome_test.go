package thump_test

import (
	"testing"
	"time"

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

func withoutDecisionRef(o thump.Outcome) thump.Outcome {
	o.DecisionRef = ""
	return o
}

func withoutExecutedAt(o thump.Outcome) thump.Outcome {
	o.ExecutedAt = time.Time{}
	return o
}

func withoutMode(o thump.Outcome) thump.Outcome {
	o.Mode = ""
	return o
}

func silentFailure(o thump.Outcome) thump.Outcome {
	o.Result, o.Error = thump.ResultFailure, ""
	return o
}

func explainedPartial(o thump.Outcome) thump.Outcome {
	o.Result, o.Error = thump.ResultPartialNonConverging, "latency recovered; error rate did not"
	return o
}

func silentPartial(o thump.Outcome) thump.Outcome {
	o.Result, o.Error = thump.ResultPartialNonConverging, ""
	return o
}

func goldenOutcome() thump.Outcome {
	return thump.Outcome{
		ID:          "out:slo_burn:ceph-rgw:1000",
		DecisionRef: "dec:slo_burn:ceph-rgw:1000",
		SignalRef:   "slo_burn:ceph-rgw",
		ContractRef: "throttle-non-critical-paths",
		Mode:        thump.ModeDryRun,     // the honest half…
		Result:      thump.ResultRendered, // …of the honest whole: we rehearsed, we did not act
		Error:       "",
		ExecutedAt:  frozenNow(),
	}
}

func frozenNow() time.Time { return time.Unix(1000, 0) }
