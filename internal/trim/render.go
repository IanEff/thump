package trim

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

var (
	dimStyle    = lipgloss.NewStyle().Faint(true)
	dangerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e78284"))
)

// renderIncident renders one Incident as a single, operator-facing line.
func renderIncident(inc Incident, now time.Time) string {
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
	if inc.Stage == StageHeld {
		since := now.Sub(inc.UpdatedAt)
		sb.WriteString("  held ")
		sb.WriteString(since.String())
	}
	if inc.Forced {
		sb.WriteString("  ")
		sb.WriteString(dangerStyle.Render("FORCED"))
		sb.WriteString(" by ")
		sb.WriteString(inc.Operator)
	}
	return sb.String()
}

func renderIncidents(incidents []Incident, now time.Time) string {
	lines := make([]string, 0, len(incidents))
	for _, inc := range incidents {
		lines = append(lines, renderIncident(inc, now))
	}
	return strings.Join(lines, "\n")
}
