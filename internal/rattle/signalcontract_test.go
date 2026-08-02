package rattle_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
)

func TestSignalContract_Attenuated(t *testing.T) {
	t.Parallel()
	contract := rattle.SignalContract{
		ConfidenceFloor: 0.1,
		ExclusionWindows: []rattle.ExclusionWindow{
			{Start: time.Unix(1000, 0), End: time.Unix(2000, 0), Reason: "scheduled maintenance"},
		},
	}
	cases := map[string]struct {
		at   time.Time
		want float64
	}{
		"Attenuated returns the base confidence outside any exclusion window": {
			time.Unix(500, 0), 0.9,
		},
		"Attenuated lowers but never zeroes confidence inside an exclusion window": {
			time.Unix(1500, 0), 0.45, // halved, not suppressed — and still > ConfidenceFloor
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := contract.Attenuated(0.9, tc.at)
			if got != tc.want {
				t.Error("attenuation produced the wrong confidence", cmp.Diff(tc.want, got))
			}
			if got <= 0 {
				t.Error("attenuation must never suppress to zero — that's the rule this test exists to pin")
			}
		})
	}
}

func TestSignalContract_Fresh(t *testing.T) {
	t.Parallel()
	contract := rattle.SignalContract{FreshnessBound: 5 * time.Minute}
	now := time.Unix(10_000, 0)

	cases := map[string]struct {
		window []rattle.Sample
		want   bool
	}{
		"Fresh reports false on an empty window": {nil, false},
		"Fresh reports true when newest sample is inside FreshnessBound": {
			[]rattle.Sample{{T: now.Add(-4 * time.Minute)}}, true,
		},
		"Fresh reports false when newest sample is older than FreshnessBound": {
			[]rattle.Sample{{T: now.Add(-6 * time.Minute)}}, false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := contract.Fresh(tc.window, now)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("Fresh reported the wrong... freshness", diff)
			}
		})
	}
}
