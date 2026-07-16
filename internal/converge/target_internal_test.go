package converge

import "testing"

// TestParseTarget pins how a SuccessCriteria.Target prose string ("p99 <
// 250ms", "HEALTH_OK") becomes a comparator + threshold. This is Wave D's
// road (A): parse the prose rather than grow the api/v1 schema — see
// phase-f-break-the-seal-guide.md Part 5, Piece 2.
func TestParseTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		target        string
		wantOp        string
		wantThreshold float64
		wantErr       bool
	}{
		{name: "p99 latency target", target: "p99 < 250ms", wantOp: "<", wantThreshold: 250},
		{name: "avg latency target", target: "avg < 50ms", wantOp: "<", wantThreshold: 50},
		{name: "ceph health special case", target: "HEALTH_OK", wantOp: "==", wantThreshold: 0},
		{name: "decimal threshold", target: "p99 < 12.5ms", wantOp: "<", wantThreshold: 12.5},
		{name: "empty target errors", target: "", wantErr: true},
		{name: "unrecognized prose errors", target: "somewhere between fine and not", wantErr: true},
		{name: "unsupported operator errors", target: "p99 <= 250ms", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			op, threshold, err := parseTarget(tc.target)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseTarget(%q) = (%q, %v, <nil>), want an error", tc.target, op, threshold)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTarget(%q) returned unexpected error: %v", tc.target, err)
			}
			if op != tc.wantOp || threshold != tc.wantThreshold {
				t.Errorf("parseTarget(%q) = (%q, %v), want (%q, %v)", tc.target, op, threshold, tc.wantOp, tc.wantThreshold)
			}
		})
	}
}
