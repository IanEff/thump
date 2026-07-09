package beat

import (
	"testing"
	"time"
)

// TestNextDelay pins the backoff growth as a pure function: reset to Base on a
// good tick, double on a bad one, and never exceed Cap.
func TestNextDelay(t *testing.T) {
	t.Parallel()
	cfg := BackoffConfig{Base: 5 * time.Second, Cap: 5 * time.Minute, JitterDivisor: 4}

	tests := []struct {
		name   string
		cur    time.Duration
		tickOK bool
		want   time.Duration
	}{
		{"success resets to base", 2 * time.Minute, true, 5 * time.Second},
		{"failure doubles", 10 * time.Second, false, 20 * time.Second},
		{"failure is capped", 4 * time.Minute, false, 5 * time.Minute},
		{"failure at cap stays at cap", 5 * time.Minute, false, 5 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextDelay(cfg, tc.cur, tc.tickOK); got != tc.want {
				t.Errorf("nextDelay(%v, ok=%v) = %v, want %v", tc.cur, tc.tickOK, got, tc.want)
			}
		})
	}
}
