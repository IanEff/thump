package calipers

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/ianeff/thump/internal/incident"
)

var (
	dimStyle    = lipgloss.NewStyle().Faint(true)
	dangerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e78284"))
)

// renderIncident renders one Incident as a single, operator-facing line.
func renderIncident(inc incident.Incident, now time.Time) string {
	var sb strings.Builder

	sb.WriteString(inc.Fingerprint)
	sb.WriteString("  ")
	sb.WriteString(inc.Service)
	sb.WriteString("  ")
	sb.WriteString(string(inc.Stage))
	sb.WriteString("  severity=")
	if inc.Severity == nil {
		sb.WriteString(dimStyle.Render("unmeasured"))
	} else {
		_, _ = fmt.Fprintf(&sb, "%.2f", *inc.Severity)
	}
	if inc.Governed != nil && inc.Governed.Decision.Verdict.AwaitsApproval() {
		since := now.Sub(inc.UpdatedAt)
		sb.WriteString(" held ")
		sb.WriteString(since.String())
	}
	if inc.Governed != nil && inc.Governed.Decision.Forced {
		sb.WriteString(" ")
		sb.WriteString(dangerStyle.Render("FORCED"))
		sb.WriteString(" by ")
		sb.WriteString(inc.Governed.Decision.Operator)
	} else if inc.Governed != nil && inc.Governed.Decision.Approver != "" {
		sb.WriteString(" approved by ")
		sb.WriteString(inc.Governed.Decision.Approver)
	}
	return sb.String()
}

func renderIncidents(incidents []incident.Incident, now time.Time) string {
	lines := make([]string, 0, len(incidents))
	for _, inc := range incidents {
		lines = append(lines, renderIncident(inc, now))
	}
	return strings.Join(lines, "\n")
}

// renderIncidentDetail renders one Incident as a kubectl-describe-shaped
// block over the whole ranked proposal.Set, not just the recommended
// Candidate — the charter calls the set the audit unit, so the detail view
// shows a human everything the engine actually decided among.
func renderIncidentDetail(inc incident.Incident, now time.Time) string {
	var sb strings.Builder

	_, _ = fmt.Fprintf(&sb, "Fingerprint:  %s\n", inc.Fingerprint)
	_, _ = fmt.Fprintf(&sb, "Service:      %s\n", inc.Service)
	_, _ = fmt.Fprintf(&sb, "Stage:        %s\n", inc.Stage)
	_, _ = fmt.Fprintf(&sb, "Updated:      %s (%s ago)\n", inc.UpdatedAt.Format(time.RFC3339), now.Sub(inc.UpdatedAt))
	if inc.Severity != nil {
		_, _ = fmt.Fprintf(&sb, "Severity:     %.2f\n", *inc.Severity)
	} else {
		_, _ = fmt.Fprintf(&sb, "Severity:     %s\n", dimStyle.Render("unmeasured"))
	}

	if inc.Governed == nil {
		sb.WriteString("\nNo decision recorded yet.\n")
		return sb.String()
	}

	d := inc.Governed.Decision
	_, _ = fmt.Fprintf(&sb, "\nVerdict:      %s\n", d.Verdict)
	if d.Forced {
		_, _ = fmt.Fprintf(&sb, "              %s by %s\n", dangerStyle.Render("FORCED"), d.Operator)
	} else if d.Approver != "" {
		_, _ = fmt.Fprintf(&sb, "              approved by %s\n", d.Approver)
	}
	if len(d.Reasons) > 0 {
		_, _ = fmt.Fprintf(&sb, "Reasons:      %s\n", strings.Join(d.Reasons, ", "))
	}

	set := inc.Governed.Set
	_, _ = fmt.Fprintf(&sb, "\nProposals (%d), recommended=%s:\n", len(set.Proposals), set.Recommended)
	for _, c := range set.Proposals {
		marker := "  "
		if c.ID == set.Recommended {
			marker = "* "
		}
		_, _ = fmt.Fprintf(&sb, "%s#%d  %-30s confidence=%.2f computed=%.2f ceiling-bound=%t\n",
			marker, c.Rank, c.ContractRef, c.Confidence, c.ComputedConfidence, c.ConfidenceCeilingBound)
	}

	return sb.String()
}
