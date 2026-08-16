package probe

import (
	"fmt"
	"io"
)

// Spread is a fold's min/mean/max over one term across every counted run —
// enough to see both the center and how wide the model's self-report swings
// run to run, which a single averaged number hides.
type Spread struct {
	Min, Mean, Max float64
}

// Summary is a fold over a probe sample — how many runs proposed nothing at
// all, and, among the ones that did, the spread of the model's own
// self-reported Confidence and its Terms.Corroborated count, plus how many
// runs cleared floor. Summarize forms no judgement of its own: floor is
// operator-supplied, the same stance harvest.Main and tune's NotYet take
// toward their own inputs — this only counts, folds, and prints.
type Summary struct {
	N int
	// NoProposal counts runs whose recommended candidate never resolved
	// (insufficient, budget-exhausted, or a caught error) — these carry no
	// Confidence or Corroborated term to fold in, and count toward
	// UnderFloor automatically, since a run that proposed nothing cleared no
	// floor.
	NoProposal   int
	Floor        float64
	UnderFloor   int
	Confidence   Spread
	Corroborated Spread
}

// Summarize folds rows into a Summary against floor. Only rows carrying a
// recommended candidate (ContractRef != "") contribute to Confidence and
// Corroborated — a row with nothing proposed has no self-report to fold in.
func Summarize(rows []Row, floor float64) Summary {
	s := Summary{N: len(rows), Floor: floor}

	var confSum, corrSum float64
	counted := 0
	for _, r := range rows {
		if r.ContractRef == "" {
			s.NoProposal++
			s.UnderFloor++
			continue
		}
		corroborated := float64(r.Terms.Corroborated)
		if counted == 0 {
			s.Confidence.Min, s.Confidence.Max = r.Confidence, r.Confidence
			s.Corroborated.Min, s.Corroborated.Max = corroborated, corroborated
		} else {
			s.Confidence.Min = min(s.Confidence.Min, r.Confidence)
			s.Confidence.Max = max(s.Confidence.Max, r.Confidence)
			s.Corroborated.Min = min(s.Corroborated.Min, corroborated)
			s.Corroborated.Max = max(s.Corroborated.Max, corroborated)
		}
		confSum += r.Confidence
		corrSum += corroborated
		counted++
		if r.Confidence < floor {
			s.UnderFloor++
		}
	}
	if counted > 0 {
		s.Confidence.Mean = confSum / float64(counted)
		s.Corroborated.Mean = corrSum / float64(counted)
	}
	return s
}

// RenderSummary writes s as the one-line fold `calipers probe` prints after
// its per-run table — the number an operator actually wants before reading
// any row: how many of n runs cleared floor, and how wide the model's own
// self-report swung getting there.
func RenderSummary(w io.Writer, s Summary) {
	_, _ = fmt.Fprintf(w, "n=%d underFloor=%d/%d (floor=%.2f) noProposal=%d confidence[min=%.2f mean=%.2f max=%.2f] corroborated[min=%.1f mean=%.1f max=%.1f]\n",
		s.N, s.UnderFloor, s.N, s.Floor, s.NoProposal,
		s.Confidence.Min, s.Confidence.Mean, s.Confidence.Max,
		s.Corroborated.Min, s.Corroborated.Mean, s.Corroborated.Max)
}
