package rattle_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/rattle"
)

func multiSignal(series map[string][]float64) rattle.MultiSignalWindow {
	out := make(rattle.MultiSignalWindow, len(series))
	for name, rates := range series {
		out[name] = window(rates...)
	}
	return out
}

func TestCorrelationDetector(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		in   rattle.MultiSignalWindow
		want bool
	}{
		"Fires returns false when only one signal worsens": {
			multiSignal(map[string][]float64{
				"retries": {1, 2, 3, 4}, "timeouts": {1, 1, 1, 1}, "fallbacks": {2, 2, 2, 1},
			}), false,
		},
		"Fires returns true when two signals worsen together": {
			multiSignal(map[string][]float64{
				"retries": {1, 2, 3, 4}, "timeouts": {1, 2, 3, 4}, "fallbacks": {2, 2, 2, 1},
			}), true,
		},
		"Fires returns false when all signals are flat": {
			multiSignal(map[string][]float64{
				"retries": {1, 1, 1, 1}, "timeouts": {1, 1, 1, 1}, "fallbacks": {1, 1, 1, 1},
			}), false,
		},
	}
	d := rattle.CorrelationDetector{MinSignals: 2}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := d.Fires(tc.in)
			if got != tc.want {
				t.Error("detector misjudged signal co-movement", cmp.Diff(tc.want, got))
			}
		})
	}
}
