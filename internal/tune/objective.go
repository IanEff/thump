// Package tune sweeps clank's scoring weights over recorded runs and proposes
// a diff.
package tune

import (
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/rca"
)

// Objective is what a sweep maximizes, stated as data so a report can print
// it beside its own numbers — an RCA score and a floor's admitted-wins are
// different questions, and blending them into one number is the
// gate-vs-shaper mistake applied to tuning.
type Objective struct {
	// Grounded counts rows reaching the right failure class by citing the
	// evidence that distinguishes it from the decoy.
	Grounded func(rca.Report) int

	// Support reads the committed corpus at a candidate floor — admitted
	// wins against refused wins, never a win rate.
	Support func(clank.Corpus, proposal.FailureClass, float64) clank.FloorSupport
}

// DefaultObjective wires the two questions to the code that already answers
// them — Corpus.FloorSupport already skips rendered and applied outcomes and
// counts refused wins separately, so it is handed in, never reimplemented.
func DefaultObjective() Objective {
	return Objective{
		Grounded: func(r rca.Report) int { return r.Scored },
		Support: func(c clank.Corpus, class proposal.FailureClass, floor float64) clank.FloorSupport {
			return c.FloorSupport(class, floor)
		},
	}
}
