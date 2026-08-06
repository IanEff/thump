// Package replay re-runs a recorded reason loop against a real clank.Engine
// without reaching a provider. It reconstructs nothing it cannot read: the
// EvidenceRefs come from the run's emitted proposal.Set, never from parsing a
// tool digest, because Live and Subject are not in the transcript at all and a
// guessed grounding tier produces a confidence number that is wrong and looks
// right.
package replay

import (
	"context"
	"errors"

	"github.com/ianeff/thump/internal/reason"
)

// ErrTranscriptExhausted means the loop asked for a turn the recording does
// not hold.
var ErrTranscriptExhausted = errors.New("replay: the loop asked for a turn this transcript does not hold")

// Model replays a recorded run's assistant completions in order.
type Model struct {
	completions []reason.Completion
	pos int
}

func NewModel(completions []reason.Completion) *Model {
	return &Model{completions: completions}
}

// Complete returns the next recorded completion, ignoring msgs and
// tools entirely.
func (m *Model) Complete(_ context.Context, _ []reason.Message, _ reason.ToolSpec) (reason.Completion, error) {
	if m.pos >= len(m.completions) {
		return reason.Completion{}, ErrTranscriptExhausted
	}
	c := m.completions[m.pos]
	m.pos++
	return c, nil
}

