package outcome_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/outcome"
	"go.yaml.in/yaml/v2"
)

func TestOutcome_ObservedSeverityRoundTrips(t *testing.T) {
	t.Parallel()
	sev := 0.42
	cases := map[string]outcome.Outcome{
		"a measured severity survives the wire as a non-nil pointer": {
			DecisionRef: "dec:x:1", Mode: outcome.ModeLive, Result: outcome.ResultSuccess,
			ExecutedAt: time.Unix(1000, 0), ObservedSeverity: &sev,
		},
		"an unmeasured severity survives the wire as nil, not 0.0": {
			DecisionRef: "dec:x:1", Mode: outcome.ModeLive, Result: outcome.ResultSuccess,
			ExecutedAt: time.Unix(1000, 0), ObservedSeverity: nil,
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := yaml.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var got outcome.Outcome
			if err := yaml.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Error("ObservedSeverity did not survive the wire (-want +got)", diff)
			}
		})
	}
}
