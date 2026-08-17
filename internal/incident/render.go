package incident

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// Render formats Record as an unadorned, human-readable incident audit artifact
// to w — printing no aggregate rate, no external incident comparison, and no
// HTML. Every section corresponds directly to a beat's raw boundary object on the
// stream, preserving the exact audit trail for operator judgment.
func Render(w io.Writer, r Record) error {
	var b strings.Builder

	b.WriteString("================================================================================\n")
	fmt.Fprintf(&b, "INCIDENT AUDIT RECORD: %s\n", r.Fingerprint)
	fmt.Fprintf(&b, "Service: %s | Stage: %s | Updated: %s\n",
		r.Service, r.Stage, formatTime(r.UpdatedAt))
	b.WriteString("================================================================================\n\n")

	// 1. WHAT FIRED
	renderDetection(&b, r.Detected)

	// 2. WHAT IT LOOKED AT
	renderEvidence(&b, r.Proposed)

	// 3. WHAT IT PROPOSED & DECLINED
	renderProposals(&b, r.Proposed)

	// 4. WHAT GOVERNANCE RULED
	renderGovernance(&b, r.Decided)

	// 5. WHAT RAN
	renderOrder(&b, r)

	// 6. WHAT HAPPENED
	renderOutcomes(&b, r.Settled, r.Detected)

	_, err := io.WriteString(w, b.String())
	return err
}

func renderDetection(b *strings.Builder, d *signal.Detection) {
	b.WriteString("=== 1. WHAT FIRED (Detection) ===\n")
	if d == nil {
		b.WriteString("  No detection recorded on stream.\n\n")
		return
	}

	fmt.Fprintf(b, "  Fingerprint:   %s\n", d.Fingerprint)
	fmt.Fprintf(b, "  Origin Service:%s (Tier: %s)\n", padLeft(d.OriginService), d.ServiceTier)
	fmt.Fprintf(b, "  Detector Type: %s\n", d.DetectorType)
	fmt.Fprintf(b, "  Detected At:   %s\n", formatTime(d.DetectedAt))

	b.WriteString("  Divergence:\n")
	fmt.Fprintf(b, "    Metric:      %s\n", d.Divergence.Metric)
	fmt.Fprintf(b, "    Observed:    %.4f (Baseline: %.4f)\n", d.Divergence.Observed, d.Divergence.Baseline)
	fmt.Fprintf(b, "    Confidence:  %.2f (Trajectory: %s)\n", d.Divergence.Confidence, d.Divergence.Trajectory)

	b.WriteString("  Impact:\n")
	fmt.Fprintf(b, "    Severity:    Degradation=%.2f%% (Trajectory: %s)\n",
		d.Impact.Severity.DegradationPct*100, d.Impact.Severity.Trajectory)
	fmt.Fprintf(b, "    Blast:       Affected=%.2f%%, Velocity=%s, Downstream=%d\n\n",
		d.Impact.BlastRadius.AffectedPct*100, d.Impact.BlastRadius.Velocity, d.Impact.BlastRadius.DownstreamConsumers)
}

func renderEvidence(b *strings.Builder, p *proposal.Set) {
	b.WriteString("=== 2. WHAT IT LOOKED AT (Evidence) ===\n")
	if p == nil || len(p.Evidence) == 0 {
		b.WriteString("  No evidence references recorded.\n\n")
		return
	}

	for _, ev := range p.Evidence {
		liveStr := "historical"
		if ev.Live {
			liveStr = "live"
		}
		fmt.Fprintf(b, "  - [%s] tool=%s source=%s subject=%s\n",
			ev.Key, ev.Tool, liveStr, ev.Subject)
		if ev.Query != "" {
			fmt.Fprintf(b, "    Query:   %s\n", ev.Query)
		}
		if ev.Summary != "" {
			fmt.Fprintf(b, "    Summary: %s\n", ev.Summary)
		}
		if ev.Ref != "" {
			fmt.Fprintf(b, "    Ref:     %s\n", ev.Ref)
		}
	}
	b.WriteString("\n")
}

func renderProposals(b *strings.Builder, p *proposal.Set) {
	b.WriteString("=== 3. WHAT IT PROPOSED & DECLINED (Ranked Candidates) ===\n")
	if p == nil {
		b.WriteString("  No proposal set recorded on stream.\n\n")
		return
	}

	if p.FailureClass != "" {
		fmt.Fprintf(b, "  Leading Hypothesis: %s\n", p.FailureClass)
	}
	if len(p.Hypotheses) > 0 {
		b.WriteString("  Competing Hypotheses:\n")
		for _, h := range p.Hypotheses {
			fmt.Fprintf(b, "    - %s (weight: %.2f)\n", h.Name, h.Weight)
		}
	}

	b.WriteString("  Candidates:\n")
	for _, c := range p.Proposals {
		recMarker := ""
		if c.ID == p.Recommended {
			recMarker = " [RECOMMENDED]"
		}
		fmt.Fprintf(b, "    Rank %d: %s (Contract: %s)%s\n", c.Rank, c.ID, c.ContractRef, recMarker)
		fmt.Fprintf(b, "      Confidence: emitted=%.2f, computed=%.2f (ceilingBound=%t)\n",
			c.Confidence, c.ComputedConfidence, c.ConfidenceCeilingBound)
		fmt.Fprintf(b, "      Blast Tier: %s\n", c.BlastTier)

		if c.ReversalPath != nil {
			autoStr := "manual"
			if c.ReversalPath.Automatic {
				autoStr = "automatic"
			}
			fmt.Fprintf(b, "      Reversal:   %s (%s, watching: %s, trigger: %s)\n",
				autoStr, c.ReversalPath.Method, c.ReversalPath.Watching, c.ReversalPath.Trigger)
		} else {
			b.WriteString("      Reversal:   none (irreversible)\n")
		}

		if len(c.Citations) > 0 {
			fmt.Fprintf(b, "      Citations:  %s\n", strings.Join(c.Citations, ", "))
		}
	}

	if len(p.CausalScores) > 0 {
		b.WriteString("  Causal Scores:\n")
		for _, cs := range p.CausalScores {
			fmt.Fprintf(b, "    - Event: %s (inTopology=%t, liveCorroborated=%t, likelihood=%.4f)\n",
				cs.EventID, cs.InTopology, cs.LiveCorroborated, cs.Likelihood)
			fmt.Fprintf(b, "      Components: temporal=%.4f, topological=%.4f, historical=%.4f\n",
				cs.Temporal, cs.Topological, cs.Historical)
			for _, rat := range cs.Rationale {
				fmt.Fprintf(b, "        * %s\n", rat)
			}
		}
	}
	b.WriteString("\n")
}

func renderGovernance(b *strings.Builder, d *decision.Decision) {
	b.WriteString("=== 4. WHAT GOVERNANCE RULED ===\n")
	if d == nil {
		b.WriteString("  No governance decision recorded on stream.\n\n")
		return
	}

	fmt.Fprintf(b, "  Verdict:        %s\n", d.Verdict)
	reasonsStr := "none (approved)"
	if len(d.Reasons) > 0 {
		reasonsStr = strings.Join(d.Reasons, ", ")
	}
	fmt.Fprintf(b, "  Reasons:        %s\n", reasonsStr)
	fmt.Fprintf(b, "  Risk Band:      %s (Requested: %s, Granted: %s)\n",
		d.RiskBand, d.RequestedBand, d.GrantedBand)
	fmt.Fprintf(b, "  Floor Applied:  %.2f\n", d.FloorApplied)
	fmt.Fprintf(b, "  Policy Version: %s\n", d.PolicyVersion)
	fmt.Fprintf(b, "  Evaluated At:   %s\n", formatTime(d.EvaluatedAt))

	if d.Forced {
		fmt.Fprintf(b, "  Forced:         true (Operator: %s)\n", d.Operator)
	}
	if d.Approver != "" {
		fmt.Fprintf(b, "  Approver:       %s\n", d.Approver)
	}
	b.WriteString("\n")
}

func renderOrder(b *strings.Builder, r Record) {
	b.WriteString("=== 5. WHAT RAN ===\n")
	if r.Decided == nil || (r.Decided.Verdict != decision.VerdictApproved && !r.Decided.Forced) {
		b.WriteString("  No action executed (verdict was not approved).\n\n")
		return
	}

	candidateRef := r.Decided.CandidateRef
	contractRef := ""
	if r.Proposed != nil {
		contractRef = r.Proposed.ContractRefFor(candidateRef)
	}

	fmt.Fprintf(b, "  Candidate Ref: %s\n", candidateRef)
	if contractRef != "" {
		fmt.Fprintf(b, "  Contract Ref:  %s\n", contractRef)
	}
	b.WriteString("\n")
}

func renderOutcomes(b *strings.Builder, settled []outcome.Outcome, d *signal.Detection) {
	b.WriteString("=== 6. WHAT HAPPENED (Outcomes & Fired SLI) ===\n")
	if len(settled) == 0 {
		b.WriteString("  No execution outcomes recorded on stream.\n\n")
		return
	}

	metricName := "unrecorded"
	if d != nil && d.SLORef != "" {
		metricName = d.SLORef
	} else if d != nil && d.Divergence.Metric != "" {
		metricName = d.Divergence.Metric
	}

	for i, o := range settled {
		sevStr := "unmeasured"
		if o.ObservedSeverity != nil {
			sevStr = fmt.Sprintf("%.4f", *o.ObservedSeverity)
		}

		fmt.Fprintf(b, "  Outcome #%d:\n", i+1)
		fmt.Fprintf(b, "    Contract:          %s (Mode: %s)\n", o.ContractRef, o.Mode)
		fmt.Fprintf(b, "    Claimed Result:    %s\n", o.Result)
		fmt.Fprintf(b, "    Observed Severity: %s\n", sevStr)
		fmt.Fprintf(b, "    Fired SLI Reading: metric=%s, postActionSeverity=%s\n",
			metricName, sevStr)
		if o.Error != "" {
			fmt.Fprintf(b, "    Error:             %s\n", o.Error)
		}
		fmt.Fprintf(b, "    Executed At:       %s\n", formatTime(o.ExecutedAt))
	}
	b.WriteString("\n")
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format(time.RFC3339)
}

func padLeft(s string) string {
	if s == "" {
		return " "
	}
	return " " + s
}
