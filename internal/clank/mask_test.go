package clank_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
)

func TestIdentifierMasker_MasksAndRestoresRegisteredNames(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		registered []string
		text       string
		wantMasked string
	}{
		"a single registered pod name is replaced with its placeholder": {
			registered: []string{"cart-7d9f-abcde"},
			text:       "pod cart-7d9f-abcde is CrashLoopBackOff",
			wantMasked: "pod {{mask-1}} is CrashLoopBackOff",
		},
		"a longer name is masked before its shorter prefix so cart-db never leaks through cart": {
			registered: []string{"cart-db", "cart"},
			text:       "cart-db is degraded, cart is healthy",
			wantMasked: "{{mask-1}} is degraded, {{mask-2}} is healthy",
		},
		"an unregistered name is left untouched": {
			registered: []string{"cart"},
			text:       "checkout is fine",
			wantMasked: "checkout is fine",
		},
		"a registered name does not mask a word it happens to be a substring of": {
			registered: []string{"prod"},
			text:       "production traffic is nominal",
			wantMasked: "production traffic is nominal",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := clank.NewIdentifierMaskerForTest()
			for _, r := range tc.registered {
				m.RegisterForTest(r)
			}
			gotMasked := m.MaskForTest(tc.text)
			if diff := cmp.Diff(tc.wantMasked, gotMasked); diff != "" {
				t.Error("wrong masked text (-want +got)\n", diff)
			}
			if diff := cmp.Diff(tc.text, m.UnmaskForTest(gotMasked)); diff != "" {
				t.Error("mask/unmask did not round-trip to the original text (-want +got)\n", diff)
			}
		})
	}
}

func TestIdentifierMasker_UnmasksTenOrMorePlaceholdersWithoutPrefixCollision(t *testing.T) {
	t.Parallel()
	m := clank.NewIdentifierMaskerForTest()
	names := make([]string, 12)
	for i := range names {
		names[i] = "node-" + string(rune('a'+i))
		m.RegisterForTest(names[i])
	}
	text := strings.Join(names, " ")
	masked := m.MaskForTest(text)
	if diff := cmp.Diff(text, m.UnmaskForTest(masked)); diff != "" {
		t.Error("placeholders did not round-trip cleanly with twelve names registered (-want +got)\n", diff)
	}
}

func TestMaskingModel_Complete_SendsPlaceholdersToTheWrappedModelAndRestoresRealNamesInTheResponse(t *testing.T) {
	t.Parallel()
	fake := &fakeModel{script: []clank.Completion{
		{Message: clank.Message{Role: "assistant", Content: "{{mask-1}} looks healthy"}},
	}}
	mask := clank.NewIdentifierMaskerForTest()
	mask.RegisterForTest("cart")
	mm := clank.NewMaskingModelForTest(fake, mask)

	comp, err := mm.Complete(t.Context(), []clank.Message{{Role: "user", Content: "investigate cart"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("investigate {{mask-1}}", fake.received[0][0].Content); diff != "" {
		t.Error("the wrapped model saw a real name instead of a placeholder (-want +got)\n", diff)
	}
	if diff := cmp.Diff("cart looks healthy", comp.Message.Content); diff != "" {
		t.Error("the returned completion still carries a placeholder instead of the real name (-want +got)\n", diff)
	}
}

func TestMaskingModel_Complete_NeverMutatesTheCallersMessageSlice(t *testing.T) {
	t.Parallel()
	fake := &fakeModel{script: []clank.Completion{{Message: clank.Message{Role: "assistant", Content: "ok"}}}}
	mask := clank.NewIdentifierMaskerForTest()
	mask.RegisterForTest("cart")
	mm := clank.NewMaskingModelForTest(fake, mask)

	msgs := []clank.Message{{Role: "user", Content: "investigate cart"}}
	if _, err := mm.Complete(t.Context(), msgs, nil); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("investigate cart", msgs[0].Content); diff != "" {
		t.Error("Complete mutated the caller's Message in place — Store checkpoints this exact slice (-want +got)\n", diff)
	}
}

func TestMaskingModel_Complete_RestoresPlaceholdersInsideToolCallArgsSoTheRealToolCanDispatchThem(t *testing.T) {
	t.Parallel()
	fake := &fakeModel{script: []clank.Completion{{
		ToolCalls: []clank.ToolCall{{ID: "1", Name: "kube", Args: []byte(`{"namespace":"{{mask-1}}"}`)}},
	}}}
	mask := clank.NewIdentifierMaskerForTest()
	mask.RegisterForTest("prod")
	mm := clank.NewMaskingModelForTest(fake, mask)

	comp, err := mm.Complete(t.Context(), []clank.Message{{Role: "user", Content: "check prod"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(`{"namespace":"prod"}`, string(comp.ToolCalls[0].Args)); diff != "" {
		t.Error("tool call args still carry a placeholder — the real kube tool cannot query a namespace that doesn't exist (-want +got)\n", diff)
	}
}
