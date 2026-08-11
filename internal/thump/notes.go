package thump

import (
	"fmt"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
)

// renderNotes renders the whole ranked Set a governed approval came from —
// every candidate considered, its confidence and citations, and why the
// winner won — not just the winning Candidate's own fields. Order.Notes
// carries the result into whatever artifact a reviewer reads.
func renderNotes(ps proposal.Set) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Recommended: %s\n", ps.Recommended)
	if ps.RankingRationale != nil {
		fmt.Fprintf(&sb, "Ranked by: %s", ps.RankingRationale.DominantAxis)
		if ps.RankingRationale.VelocityWeight != "" {
			fmt.Fprintf(&sb, " (velocity: %s)", ps.RankingRationale.VelocityWeight)
		}
		sb.WriteString("\n")
	}
	if len(ps.Hypotheses) > 0 {
		sb.WriteString("\nHypotheses:\n")
		for _, h := range ps.Hypotheses {
			fmt.Fprintf(&sb, "  - %s (weight %.2f)\n", h.Name, h.Weight)
		}
	}
	sb.WriteString("\nCandidates:\n")
	for _, c := range ps.Proposals {
		marker := " "
		if c.ID == ps.Recommended {
			marker = "*"
		}
		fmt.Fprintf(&sb, "%s #%d %-30s confidence=%.2f", marker, c.Rank, c.ContractRef, c.Confidence)
		if c.ConfidenceCeilingBound {
			fmt.Fprintf(&sb, " (ceiling-bound, computed=%.2f)", c.ComputedConfidence)
		}
		if len(c.Citations) > 0 {
			fmt.Fprintf(&sb, " citations=[%s]", strings.Join(c.Citations, ", "))
		}
		sb.WriteString("\n")
	}
	if len(ps.Evidence) > 0 {
		sb.WriteString("\nEvidence:\n")
		for _, e := range ps.Evidence {
			fmt.Fprintf(&sb, "  - [%s] %s: %s\n", e.Tool, e.Query, e.Summary)
		}
	}
	return sb.String()
}
