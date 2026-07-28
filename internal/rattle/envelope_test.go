package rattle_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
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

func BenchmarkEnvelopDetector_Fires(b *testing.B) {
	b.ReportAllocs()

	sizes := map[string]int{
		"Fires evaluates 10 sample window":   10,
		"Fires evaluates 100 sample window":  100,
		"Fires evaluates 1000 sample window": 1000,
	}

	detector := rattle.EnvelopeDetector{K: 2.0}

	for name, size := range sizes {
		baseline := makeSamples(size, 1.0)
		live := makeSamples(size/2, 2.5)

		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				_ = detector.Fires(baseline, live)
			}
		})
	}
}

// makeSamples is a simple helper to generate sample windows of varying
// size
func makeSamples(count int, baseVal float64) []rattle.Sample {
	samples := make([]rattle.Sample, count)

	now := time.Now()

	for i := 0; i < count; i++ {
		samples[i] = rattle.Sample{
			T:        now.Add(time.Duration(i) * time.Second),
			BurnRate: baseVal + float64(i%5)*0.1,
		}
	}
	return samples
}
