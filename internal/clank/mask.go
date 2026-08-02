package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ianeff/thump/internal/reason"
)

// identifierMasker gives every real identifier registered against
// it a stable placeholder for the life of one reason loop run, and
// reverses the substitution on the way back.
type identifierMasker struct {
	toPlaceholder map[string]string
	toReal        map[string]string
	pattern       map[string]*regexp.Regexp
	order         []string
	next          int
}

func newIdentifierMasker() *identifierMasker {
	return &identifierMasker{
		toPlaceholder: make(map[string]string),
		toReal:        make(map[string]string),
		pattern:       make(map[string]*regexp.Regexp),
	}
}

// register assigns name a stable placeholder the first time it's seen.
func (m *identifierMasker) register(name string) {
	if name == "" {
		return
	}
	if _, ok := m.toPlaceholder[name]; ok {
		return
	}
	m.next++
	ph := fmt.Sprintf("{{mask-%d}}", m.next)
	m.toPlaceholder[name] = ph
	m.toReal[ph] = name
	m.pattern[name] = regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	m.order = append(m.order, name)
}

// mask replaces every registered identifier in s with its placeholder.
func (m *identifierMasker) mask(s string) string {
	names := append([]string(nil), m.order...)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	for _, name := range names {
		s = m.pattern[name].ReplaceAllString(s, m.toPlaceholder[name])
	}
	return s
}

// unmask reverses mask.  Order never matters.
func (m *identifierMasker) unmask(s string) string {
	for ph, name := range m.toReal {
		s = strings.ReplaceAll(s, ph, name)
	}
	return s
}

// maskMessages returns a masked copy of msgs.
func (m *identifierMasker) maskMessages(msgs []reason.Message) []reason.Message {
	out := make([]reason.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		out[i].Content = m.mask(msg.Content)
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = make([]reason.ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				tc.Args = json.RawMessage(m.mask(string(tc.Args)))
				out[i].ToolCalls[j] = tc
			}
		}
		if len(msg.ToolResults) > 0 {
			out[i].ToolResults = make([]reason.ToolResult, len(msg.ToolResults))
			for j, tr := range msg.ToolResults {
				tr.Digest = m.mask(tr.Digest)
				out[i].ToolResults[j] = tr
			}
		}
	}
	return out
}

// unmaskCompletion restores real names into every text field a reason.Model
// response can carry, including ToolCalls[].Args, which must be real
// before engine.go dispatches a tool with it.
func (m *identifierMasker) unmaskCompletion(c reason.Completion) reason.Completion {
	c.Message.Content = m.unmask(c.Message.Content)
	c.Message.ToolCalls = m.unmaskToolCalls(c.Message.ToolCalls)
	c.ToolCalls = m.unmaskToolCalls(c.ToolCalls)
	return c
}

func (m *identifierMasker) unmaskToolCalls(calls []reason.ToolCall) []reason.ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]reason.ToolCall, len(calls))
	for i, tc := range calls {
		tc.Args = json.RawMessage(m.unmask(string(tc.Args)))
		out[i] = tc
	}
	return out
}

type maskerContextKey struct{}

func contextWithMasker(ctx context.Context, m *identifierMasker) context.Context {
	return context.WithValue(ctx, maskerContextKey{}, m)
}

// registerIdentifier records name with ctx's run masker if one is
// set, silently doing nothing when it isn't so a reason.Tool exercised outside
// Engine.Propose never has to construct a masker just to run.
func registerIdentifier(ctx context.Context, name string) {
	if m, ok := ctx.Value(maskerContextKey{}).(*identifierMasker); ok {
		m.register(name)
	}
}

// maskingModel wraps a reason.Model, masking every identifier the run
// has registered before a reason.Message crosses to the wire and restoring
// them in the reason.Completion before anything else can observe it.
type maskingModel struct {
	reason.Model
	mask *identifierMasker
}

func (m *maskingModel) Complete(ctx context.Context, msgs []reason.Message, tools []reason.ToolSpec) (reason.Completion, error) {
	comp, err := m.Model.Complete(ctx, m.mask.maskMessages(msgs), tools)
	if err != nil {
		return reason.Completion{}, err
	}
	return m.mask.unmaskCompletion(comp), nil
}
