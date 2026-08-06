package rca

import (
	"slices"

	"github.com/ianeff/thump/api/v1/proposal"
)

// Row is one graded scenario's result. Computed and Emitted are kept apart
// because a row where CeilingBound is true is one no weight change can reach,
// which a sweep has to know before it starts rather than discover halfway.
type Row struct {
	Name         string
	Pass         bool
	KnownMiss    bool
	Miss         string // why it failed, empty when Pass
	Class        proposal.FailureClass
	ContractRef  string
	Computed     float64
	Emitted      float64
	CeilingBound bool
}

// Report is one run of the graded suite — the audit unit, not the top line.
// Floor counts the non-KnownMiss rows actually present in Rows, not the
// full table: a run filtered to one row must judge that row against its own
// population, or a `-row` debug run always reads as failing the whole
// suite's floor.
type Report struct {
	Rows   []Row
	Scored int
	Floor  int
}

// NewReport tallies rows and derives the floor from the rows it was given.
func NewReport(rows []Row) Report {
	r := Report{Rows: rows}
	for _, row := range rows {
		if row.Pass {
			r.Scored++
		}
		if !row.KnownMiss {
			r.Floor++
		}
	}
	return r
}

// Floor is how many rows in the full table must pass: every row that is not
// a documented known miss. A suite requiring all eight would either get a
// known miss quietly dropped or turn into a CI gate on model output.
func Floor() int {
	floor := 0
	for _, c := range Table() {
		if !c.KnownMiss {
			floor++
		}
	}
	return floor
}

// Met reports whether the run cleared its floor.
func (r Report) Met() bool { return r.Scored >= r.Floor }

// grade scores one emitted set against the row that asked for it. Citation
// discipline is graded before the verdict: a run reaching the right class off
// the decoy is a miss even though its class matches.
func grade(c Case, set proposal.Set) Row {
	row := Row{
		Name:      c.Name,
		KnownMiss: c.KnownMiss,
		Class:     set.FailureClass,
	}

	if c.WantDisposition == "insufficient" {
		if len(set.Proposals) != 0 {
			row.Miss = "wanted insufficient, got a proposal"
			return row
		}
		row.Pass = true
		return row
	}

	if len(set.Proposals) == 0 {
		row.Miss = "wanted a proposal, got insufficient"
		return row
	}

	top := set.Proposals[0]
	row.ContractRef = top.ContractRef
	row.Computed = top.ComputedConfidence
	row.Emitted = top.Confidence
	row.CeilingBound = top.ConfidenceCeilingBound

	switch {
	case set.FailureClass != c.WantClass:
		row.Miss = "wrong failure class: " + string(set.FailureClass)
	case c.WantContractRef != "" && top.ContractRef != c.WantContractRef:
		row.Miss = "wrong action: " + top.ContractRef
	case !citesAll(top.Citations, c.MustCite):
		row.Miss = "did not cite the distinguishing evidence"
	case citesAny(top.Citations, c.MustNotCite):
		row.Miss = "cited the decoy"
	case c.WantConfidenceAtLeast > 0 && top.Confidence < c.WantConfidenceAtLeast:
		row.Miss = "confidence below the row's floor"
	case c.WantConfidenceAtLeast > 0 && top.ConfidenceCeilingBound != c.WantCeilingBound:
		row.Miss = "ceiling-bound disagreed with the row"
	default:
		row.Pass = true
	}
	return row
}

func citesAll(got, want []string) bool {
	for _, w := range want {
		if !slices.Contains(got, w) {
			return false
		}
	}
	return true
}

func citesAny(got, want []string) bool {
	for _, w := range want {
		if slices.Contains(got, w) {
			return true
		}
	}
	return false
}
