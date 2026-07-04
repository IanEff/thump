package rattle_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
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
			got, _ := d.Detect(tc.in)
			if got != tc.want {
				t.Error("detector misread the burn shape", cmp.Diff(tc.want, got))
			}
		})
	}
}

func TestAccelerationDetector_IgnoresNoiseAroundAFlatBurn(t *testing.T) {
	t.Parallel()
	d := rattle.AccelerationDetector{Threshold: 0.5}
	if got, _ := d.Detect(window(1.0, 1.3, 0.8, 1.2, 1.0)); got {
		t.Error("detector fired on noise around a flat burn - threshold too low or no smoothing")
	}
}

func TestSustainedBurnDetector(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		in        []rattle.Sample
		threshold float64
		samples   int
		wantFired bool
		wantLevel float64
	}{
		"Fires on ceph-health ceiling-pin": {
			in:        window(1000, 1000, 1000, 1000, 1000),
			threshold: 1.0,
			samples:   5,
			wantFired: true,
			wantLevel: 1000,
		},
		"Stays quiet when plateau sits below threshold": {
			in:        window(0.588, 0.588, 0.588, 0.588, 0.588, 0.0, 0.726, 0.703, 0.706, 0.702),
			threshold: 1.0,
			samples:   5,
			wantFired: false,
			wantLevel: 0,
		},
		"Stays quiet when window is too short": {
			in:        window(1.5, 1.6, 1.7),
			threshold: 1.0,
			samples:   5,
			wantFired: false,
			wantLevel: 0,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := rattle.SustainedBurnDetector{Threshold: tc.threshold, MinSamples: tc.samples}
			fired, level := d.Detect(tc.in)
			if fired != tc.wantFired {
				t.Errorf("wrong fired status: want %t, got %t", tc.wantFired, fired)
			}
			if level != tc.wantLevel {
				t.Errorf("wrong level: want %f, got %f", tc.wantLevel, level)
			}
		})
	}
}
