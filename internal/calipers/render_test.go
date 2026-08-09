package calipers

import (
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/incident"
)

// TestRenderIncident_ShowsFingerprintServiceAndStage pins the baseline: the
// three facts an operator needs at a glance are always present in the
// rendered line, regardless of which stage the incident is in.
func TestRenderIncident_ShowsFingerprintServiceAndStage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)
	inc := incident.Incident{Fingerprint: "fp-1", Stage: incident.StageProposed, Service: "checkout-api", UpdatedAt: now}

	got := renderIncident(inc, now)

	for _, want := range []string{"fp-1", "checkout-api", string(incident.StageProposed)} {
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
	base := incident.Incident{Fingerprint: "fp-1", Stage: incident.StageSettled, Service: "checkout-api", UpdatedAt: now}

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
			severity:   new(0.0),
			wantSubstr: "0.00",
			wantAbsent: "unmeasured",
		},
		"renderIncident shows a real nonzero Severity": {
			severity:   new(0.62),
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
		incident   incident.Incident
		wantSubstr []string
		wantAbsent string
	}{
		"renderIncident marks a forced approval with FORCED and names the operator": {
			incident: incident.Incident{
				Fingerprint: "fp-1", Stage: incident.StageDecided, Service: "checkout-api",
				UpdatedAt: now,
				Governed: &decision.Governed{Decision: decision.Decision{
					Verdict: decision.VerdictApproved, Forced: true, Operator: "alice",
				}},
			},
			wantSubstr: []string{"FORCED", "alice"},
		},
		"renderIncident does not mark an ordinary hiss-granted approval FORCED": {
			incident: incident.Incident{
				Fingerprint: "fp-2", Stage: incident.StageDecided, Service: "checkout-api",
				UpdatedAt: now,
				Governed:  &decision.Governed{Decision: decision.Decision{Verdict: decision.VerdictApproved}},
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
		incident   incident.Incident
		wantSubstr string
		wantAbsent string
	}{
		"renderIncident shows a held incident's wait time": {
			incident: incident.Incident{
				Fingerprint: "fp-1", Stage: incident.StageDecided, Service: "checkout-api",
				UpdatedAt: now.Add(-3 * time.Minute),
				Governed:  &decision.Governed{Decision: decision.Decision{Verdict: decision.VerdictHold}},
			},
			wantSubstr: "3m0s",
		},
		"renderIncident omits the held-since line for a non-held incident": {
			incident: incident.Incident{
				Fingerprint: "fp-2", Stage: incident.StageDecided, Service: "checkout-api",
				UpdatedAt: now.Add(-3 * time.Minute),
				Governed:  &decision.Governed{Decision: decision.Decision{Verdict: decision.VerdictApproved}},
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

	incidents := []incident.Incident{
		{Fingerprint: "fp-held", Stage: incident.StageDecided, Service: "checkout-api", UpdatedAt: now.Add(-time.Minute),
			Governed: &decision.Governed{Decision: decision.Decision{Verdict: decision.VerdictHold}}},
		{Fingerprint: "fp-declined", Stage: incident.StageDecided, Service: "billing-api", UpdatedAt: now.Add(-2 * time.Minute),
			Governed: &decision.Governed{Decision: decision.Decision{Verdict: decision.VerdictRejected}}},
	}

	got := renderIncidents(incidents, now)

	for _, want := range []string{"fp-held", "fp-declined"} {
		if !strings.Contains(got, want) {
			t.Errorf("want the incident list to contain %q, got %q", want, got)
		}
	}
}

func TestRenderIncident_ShowsWhoApprovedWhenNotForced(t *testing.T) {
	t.Parallel()
	inc := incident.Incident{
		Fingerprint: "fp-1", Stage: incident.StageDecided, Service: "checkout-api",
		Governed: &decision.Governed{Decision: decision.Decision{Verdict: decision.VerdictApproved, Approver: "alice"}},
	}

	got := renderIncident(inc, time.Now())

	if !strings.Contains(got, "approved by alice") {
		t.Errorf("want the approver named in the rendered line, got %q", got)
	}
}

// TestRenderIncidentDetail_ShowsTheWholeRankedSet pins the charter's "the set
// is the audit unit" claim at the render layer: the detail view has to show
// every candidate hiss ranked, with only the recommended one marked, not just
// the winner.
func TestRenderIncidentDetail_ShowsTheWholeRankedSet(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)

	inc := incident.Incident{
		Fingerprint: "fp-1", Stage: incident.StageDecided, Service: "checkout-api", UpdatedAt: now,
		Governed: &decision.Governed{
			Decision: decision.Decision{Verdict: decision.VerdictHold},
			Set: proposal.Set{
				Recommended: "cand-1",
				Proposals: []proposal.Candidate{
					{ID: "cand-1", Rank: 1, ContractRef: "restart-pod", Confidence: 0.9, ComputedConfidence: 0.95, ConfidenceCeilingBound: true},
					{ID: "cand-2", Rank: 2, ContractRef: "scale-up", Confidence: 0.4, ComputedConfidence: 0.4, ConfidenceCeilingBound: false},
				},
			},
		},
	}

	got := renderIncidentDetail(inc, now)

	for _, want := range []string{"restart-pod", "scale-up"} {
		if !strings.Contains(got, want) {
			t.Errorf("want the detail view to mention candidate %q, got %q", want, got)
		}
	}
	if !strings.Contains(got, "* #1  restart-pod") {
		t.Errorf("want the recommended candidate marked with *, got %q", got)
	}
	if strings.Contains(got, "* #2  scale-up") {
		t.Errorf("want only the recommended candidate marked with *, got %q", got)
	}
}

// TestRenderIncidentDetail_ShowsNoDecisionRecordedYetWhenGovernedIsNil pins
// the early-return path for a fingerprint hiss hasn't ruled on yet.
func TestRenderIncidentDetail_ShowsNoDecisionRecordedYetWhenGovernedIsNil(t *testing.T) {
	t.Parallel()
	inc := incident.Incident{Fingerprint: "fp-1", Stage: incident.StageProposed, Service: "checkout-api"}

	got := renderIncidentDetail(inc, time.Now())

	if !strings.Contains(got, "No decision recorded yet.") {
		t.Errorf("want the detail view to say no decision recorded, got %q", got)
	}
}
