package replay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/reason"
)

// Transcript is one recorded run: the assistant turns it produced and the
// set it emitted. The set is not decoration — it is the only place
// EvidenceRef.Live and .Subject survive, and without them every replayed
// confidence is scored at the ungrounded tier.
type Transcript struct {
	RunID       string
	Completions []reason.Completion
	Set         proposal.Set
}

// LoadTranscript reads a run's .jsonl and, if setPath exists, its paired
// .set.json. A transcript with no sibling set loads with a zero Set rather
// than erroring — some fixtures (a truncation test's own transcript, for
// one) exist to exercise a failure that fires before the engine ever needs
// one. Turn.Msgs is cumulative — each line holds the whole history up to
// that step — so the completions are recovered from the last non-terminal
// line, not accumulated across lines.
func LoadTranscript(jsonlPath, setPath string) (Transcript, error) {
	f, err := os.Open(jsonlPath) //nolint:gosec // G304: operator-supplied fixture path, not user input
	if err != nil {
		return Transcript{}, fmt.Errorf("open transcript %s: %w", jsonlPath, err)
	}
	defer func() { _ = f.Close() }()

	var last clank.Turn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024) // a seed prompt is large
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var t clank.Turn
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			return Transcript{}, fmt.Errorf("decode transcript line: %w", err)
		}
		if t.RunID == "" {
			continue // the terminalRecord {"finished": true}
		}
		last = t
	}
	if err := sc.Err(); err != nil {
		return Transcript{}, fmt.Errorf("read transcript %s: %w", jsonlPath, err)
	}
	if last.RunID == "" {
		return Transcript{}, fmt.Errorf("%s holds no checkpointed turn", jsonlPath)
	}

	var set proposal.Set
	raw, err := os.ReadFile(setPath) //nolint:gosec // G304: operator-supplied fixture path, not user input
	switch {
	case errors.Is(err, os.ErrNotExist):
		// no sibling .set.json — Transcript.Set stays zero.
	case err != nil:
		return Transcript{}, fmt.Errorf("open set %s: %w", setPath, err)
	default:
		if err := json.Unmarshal(raw, &set); err != nil {
			return Transcript{}, fmt.Errorf("decode set %s: %w", setPath, err)
		}
	}

	return Transcript{
		RunID:       last.RunID,
		Completions: completionsFrom(last.Msgs),
		Set:         set,
	}, nil
}

// completionsFrom rebuilds the model's side of the conversation. Only
// assistant turns are replayed — the user seed and the tool results are what
// the engine itself regenerates, and feeding them back would double them.
func completionsFrom(msgs []reason.Message) []reason.Completion {
	var out []reason.Completion
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		out = append(out, reason.Completion{Message: m, ToolCalls: m.ToolCalls})
	}

	return out
}
