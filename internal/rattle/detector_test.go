package rattle_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/rattle"
)

func window(rates ...float64) []rattle.Sample {
	out := make([]rattle.Sample, len(rates))
	base := time.Unix(0, 0)
	for i, r := range rates {
		out[i] = rattle.Sample{T: base.Add(time.Duration(i) * time.Minute), BurnRate: r}
	}
	return out
}

func TestAccelerationDetector(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		in   []rattle.Sample
		want bool
	}{
		"Fires returns false for a flat burn":                  {window(1, 1, 1, 1), false},
		"Fires returns false for a steady climb":               {window(1, 2, 3, 4), false}, // THE discriminator
		"Fires returns true for an accelerating burn":          {window(1, 2, 4, 8), true},
		"Fires returns false for a high but decelerating burn": {window(8, 5, 3, 2), false},
	}
	var d rattle.AccelerationDetector

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := d.Fires(tc.in)
			if got != tc.want {
				t.Error("detector misread the burn shape", cmp.Diff(tc.want, got))
			}
		})
	}
}

func TestAccelerationDetector_IgnoresNoiseAroundAFlatBurn(t *testing.T) {
	t.Parallel()
	d := rattle.AccelerationDetector{Threshold: 0.5}
	if d.Fires(window(1.0, 1.3, 0.8, 1.2, 1.0)) {
		t.Error("detector fired on noise around a flat burn - threshold too low or no smoothing")
	}
}
