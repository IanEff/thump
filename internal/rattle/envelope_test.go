package rattle_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/rattle"
)

func TestEnvelopeDetector(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		baseline, live []rattle.Sample
		want           bool
	}{
		"Fires returns false when live stays inside the baseline envelope": {
			window(1, 1.1, 0.9, 1.0, 1.05), window(1.0, 1.1), false,
		},
		"Fires returns true when live breaches K standard deviations above baseline": {
			window(1, 1.1, 0.9, 1.0, 1.05), window(4.0, 4.2), true,
		},
		"Fires returns false when the baseline has too few samples to characterize": {
			window(1.0), window(4.0, 4.2), false, // can't compute a stddev from one point
		},
	}

	d := rattle.EnvelopeDetector{K: 2}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := d.Fires(tc.baseline, tc.live)
			if got != tc.want {
				t.Error("detector misjudged the envelope breach", cmp.Diff(tc.want, got))
			}
		})
	}
}
