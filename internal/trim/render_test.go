package trim

import (
	"strings"
	"testing"
	"time"
)

func ptr(f float64) *float64 { return &f }

// TestRenderIncident_ShowsFingerprintServiceAndStage pins the baseline: the
// three facts an operator needs at a glance are always present in the
// rendered line, regardless of which stage the incident is in.
func TestRenderIncident_ShowsFingerprintServiceAndStage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)
	inc := Incident{Fingerprint: "fp-1", Stage: StageProposed, Service: "checkout-api", UpdatedAt: now}

	got := renderIncident(inc, now)

	for _, want := range []string{"fp-1", "checkout-api", string(StageProposed)} {
		if !strings.Contains(got, want) {
			t.Errorf("want rendered incident to contain %q, got %q", want, got)
		}
	}
}

// TestRenderIncident_ShowsUnmeasuredNotZeroForNilSeverity pins the honesty
// rider at the render layer: a nil Severity has to read as "we don't know,"
// never fold into the same text as a measured 0.00.
func TestRenderIncident_ShowsUnmeasuredNotZeroForNilSeverity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)
	base := Incident{Fingerprint: "fp-1", Stage: StageSettled, Service: "checkout-api", UpdatedAt: now}

	tests := map[string]struct {
		severity   *float64
		wantSubstr string
		wantAbsent string
	}{
		"renderIncident shows unmeasured for a nil Severity": {
			severity:   nil,
			wantSubstr: "unmeasured",
		},
		"renderIncident shows a real zero Severity as 0.00, distinct from unmeasured": {
			severity:   ptr(0.0),
			wantSubstr: "0.00",
			wantAbsent: "unmeasured",
		},
		"renderIncident shows a real nonzero Severity": {
			severity:   ptr(0.62),
			wantSubstr: "0.62",
			wantAbsent: "unmeasured",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			inc := base
			inc.Severity = tc.severity
			got := renderIncident(inc, now)
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("want rendered incident to contain %q, got %q", tc.wantSubstr, got)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("want rendered incident not to contain %q, got %q", tc.wantAbsent, got)
			}
		})
	}
}

// TestRenderIncident_MarksAForcedDecisionInTheDangerStyle pins the other
// honesty rider: a forced approval must never read as an earned one. The
// literal "FORCED" marker is the TTY-independent half of that claim — the
// actual danger color is real Lip Gloss styling, confirmed by eye in a
// terminal, not by this test (color degrades to plain text off a real TTY,
// so pinning ANSI bytes here would be brittle, not rigorous).
func TestRenderIncident_MarksAForcedDecisionInTheDangerStyle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)

	tests := map[string]struct {
		incident   Incident
		wantSubstr []string
		wantAbsent string
	}{
		"renderIncident marks a forced approval with FORCED and names the operator": {
			incident: Incident{
				Fingerprint: "fp-1", Stage: StageApproved, Service: "checkout-api",
				UpdatedAt: now, Forced: true, Operator: "alice",
			},
			wantSubstr: []string{"FORCED", "alice"},
		},
		"renderIncident does not mark an ordinary hiss-granted approval FORCED": {
			incident: Incident{
				Fingerprint: "fp-2", Stage: StageApproved, Service: "checkout-api",
				UpdatedAt: now,
			},
			wantAbsent: "FORCED",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := renderIncident(tc.incident, now)
			for _, want := range tc.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("want rendered incident to contain %q, got %q", want, got)
				}
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("want rendered incident not to contain %q, got %q", tc.wantAbsent, got)
			}
		})
	}
}

// TestRenderIncident_ShowsHowLongAnIncidentHasBeenHeld pins the "since"
// line the design calls for: a held incident is stale on a clock, and how
// long it's been waiting is exactly what an operator needs to see first.
func TestRenderIncident_ShowsHowLongAnIncidentHasBeenHeld(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)

	tests := map[string]struct {
		incident   Incident
		wantSubstr string
		wantAbsent string
	}{
		"renderIncident shows a held incident's wait time": {
			incident: Incident{
				Fingerprint: "fp-1", Stage: StageHeld, Service: "checkout-api",
				UpdatedAt: now.Add(-3 * time.Minute),
			},
			wantSubstr: "3m0s",
		},
		"renderIncident omits the held-since line for a non-held incident": {
			incident: Incident{
				Fingerprint: "fp-2", Stage: StageApproved, Service: "checkout-api",
				UpdatedAt: now.Add(-3 * time.Minute),
			},
			wantAbsent: "held",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := renderIncident(tc.incident, now)
			if tc.wantSubstr != "" && !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("want rendered incident to contain %q, got %q", tc.wantSubstr, got)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("want rendered incident not to contain %q, got %q", tc.wantAbsent, got)
			}
		})
	}
}

// TestRenderIncidents_ListsDeclinesAlongsideHolds pins that a declined
// incident is first-class in the list view, not filtered out or buried next
// to the ones still waiting on a human.
func TestRenderIncidents_ListsDeclinesAlongsideHolds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)

	incidents := []Incident{
		{Fingerprint: "fp-held", Stage: StageHeld, Service: "checkout-api", UpdatedAt: now.Add(-time.Minute)},
		{Fingerprint: "fp-declined", Stage: StageDeclined, Service: "billing-api", UpdatedAt: now.Add(-2 * time.Minute)},
	}

	got := renderIncidents(incidents, now)

	for _, want := range []string{"fp-held", "fp-declined"} {
		if !strings.Contains(got, want) {
			t.Errorf("want the incident list to contain %q, got %q", want, got)
		}
	}
}
