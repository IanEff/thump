// Package mask gives every real identifier a reason loop run touches a
// stable placeholder before it crosses to the model, and restores the real
// names on the way back — so an LLM provider never sees a namespace, pod,
// or service name from the cluster it's reasoning about.
package mask

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ianeff/thump/internal/reason"
)

// IdentifierMasker gives every real identifier registered against
// it a stable placeholder for the life of one reason loop run, and
// reverses the substitution on the way back.
type IdentifierMasker struct {
	toPlaceholder map[string]string
	toReal        map[string]string
	pattern       map[string]*regexp.Regexp
	order         []string
	next          int
}

// NewIdentifierMasker returns an IdentifierMasker with nothing registered yet.
func NewIdentifierMasker() *IdentifierMasker {
	return &IdentifierMasker{
		toPlaceholder: make(map[string]string),
		toReal:        make(map[string]string),
		pattern:       make(map[string]*regexp.Regexp),
	}
}

// Register assigns name a stable placeholder the first time it's seen.
func (m *IdentifierMasker) Register(name string) {
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

// Mask replaces every registered identifier in s with its placeholder.
func (m *IdentifierMasker) Mask(s string) string {
	names := append([]string(nil), m.order...)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	for _, name := range names {
		s = m.pattern[name].ReplaceAllString(s, m.toPlaceholder[name])
	}
	return s
}

// Unmask reverses Mask.  Order never matters.
func (m *IdentifierMasker) Unmask(s string) string {
	for ph, name := range m.toReal {
		s = strings.ReplaceAll(s, ph, name)
	}
	return s
}

// maskMessages returns a masked copy of msgs.
func (m *IdentifierMasker) maskMessages(msgs []reason.Message) []reason.Message {
	out := make([]reason.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		out[i].Content = m.Mask(msg.Content)
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = make([]reason.ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				tc.Args = json.RawMessage(m.Mask(string(tc.Args)))
				out[i].ToolCalls[j] = tc
			}
		}
		if len(msg.ToolResults) > 0 {
			out[i].ToolResults = make([]reason.ToolResult, len(msg.ToolResults))
			for j, tr := range msg.ToolResults {
				tr.Digest = m.Mask(tr.Digest)
				out[i].ToolResults[j] = tr
			}
		}
	}
	return out
}

// unmaskCompletion restores real names into every text field a Model
// response can carry, including ToolCalls[].Args, which must be real
// before engine.go dispatches a tool with it.
func (m *IdentifierMasker) unmaskCompletion(c reason.Completion) reason.Completion {
	c.Message.Content = m.Unmask(c.Message.Content)
	c.Message.ToolCalls = m.unmaskToolCalls(c.Message.ToolCalls)
	c.ToolCalls = m.unmaskToolCalls(c.ToolCalls)
	return c
}

func (m *IdentifierMasker) unmaskToolCalls(calls []reason.ToolCall) []reason.ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]reason.ToolCall, len(calls))
	for i, tc := range calls {
		tc.Args = json.RawMessage(m.Unmask(string(tc.Args)))
		out[i] = tc
	}
	return out
}

type maskerContextKey struct{}

// ContextWithMasker attaches m to ctx for RegisterIdentifier to find.
func ContextWithMasker(ctx context.Context, m *IdentifierMasker) context.Context {
	return context.WithValue(ctx, maskerContextKey{}, m)
}

// RegisterIdentifier records name with ctx's run masker if one is
// set, silently doing nothing when it isn't so a Tool exercised outside
// Engine.Propose never has to construct a masker just to run.
func RegisterIdentifier(ctx context.Context, name string) {
	if m, ok := ctx.Value(maskerContextKey{}).(*IdentifierMasker); ok {
		m.Register(name)
	}
}

// maskingModel wraps a reason.Model, masking every identifier the run
// has registered before a Message crosses to the wire and restoring
// them in the Completion before anything else can observe it.
type maskingModel struct {
	reason.Model
	mask *IdentifierMasker
}

// NewMaskingModel wraps model so every call masks msgs on the way out and
// unmasks the response on the way back, using masker's registered names.
func NewMaskingModel(model reason.Model, masker *IdentifierMasker) reason.Model {
	return &maskingModel{Model: model, mask: masker}
}

func (m *maskingModel) Complete(ctx context.Context, msgs []reason.Message, tools []reason.ToolSpec) (reason.Completion, error) {
	comp, err := m.Model.Complete(ctx, m.mask.maskMessages(msgs), tools)
	if err != nil {
		return reason.Completion{}, err
	}
	return m.mask.unmaskCompletion(comp), nil
}
