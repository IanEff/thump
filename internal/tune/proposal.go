package tune

import "github.com/ianeff/thump/internal/clank"

// Proposal is one recommended change and the evidence for it — never a number
// alone. A tuned number carries its basis in the file where the next reader
// will hit it, so this renders as the comment block a human pastes above the
// value, not as a bare diff.
type Proposal struct {
	File    string  // config/clank/weights.yaml
	Key     string  // groundingOne
	From    float64 //
	To      float64 //
	Basis   string  // the corpus rows or replay deltas behind the move
	Support []clank.FloorSupport
}

// NotYet is the honest close, and it is a first-class result rather than an
// absence of one. A sweep that reports "the corpus still cannot distinguish
// these numbers" is a closed track, not a skipped one — what changes the answer
// is not the row count but whether the harvest produced refused wins or
// admitted misses, the asymmetries a floor is made of.
type NotYet struct {
	Reason  string
	Support []clank.FloorSupport
}
