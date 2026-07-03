//go:build integration
// +build integration

package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/clank"
)

func newModel(t *testing.T) clank.Model {
	t.Helper()
	key := apiKey(t)
	if key == "" {
		t.Skip("skipping integration test: ANTHROPIC_API_KEY not set (and no .env)")
	}
	return clank.NewAnthropicModel(key)
}

func apiKey(t *testing.T) string {
	t.Helper()
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
		return k
	}
	return dotenv(t)["ANTHROPIC_API_KEY"]
}

func dotenv(t *testing.T) map[string]string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}

	for {
		path := filepath.Join(dir, ".env")
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			env := map[string]string{}
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
			return env
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func callCtx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestAnthropicModel_ReturnsTextCompletionWhenNoToolsOffered(t *testing.T) {
	model := newModel(t)
	msgs := []clank.Message{
		{Role: "user", Content: "Reply with exactly the word ACK and nothing else."},
	}
	comp, err := model.Complete(callCtx(t), msgs, nil)
	if err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	if len(comp.ToolCalls) != 0 {
		t.Errorf("offered no tools, want 0 tool calls, got %d", len(comp.ToolCalls))
	}
	if !strings.Contains(comp.Message.Content, "ACK") {
		t.Errorf("want assistant content containing %q, got %q", "ACK", comp.Message.Content)
	}
	if comp.Message.Role != "assistant" {
		t.Errorf("want assistant turn role %q, got %q", "assistant", comp.Message.Role)
	}
}

func TestAnthropicModel_InvokesToolWhenPromptDemandsIt(t *testing.T) {
	model := newModel(t)

	msgs := []clank.Message{
		{Role: "user", Content: "What is the current weather in Tokyo? Use the tool."},
	}
	tools := []clank.ToolSpec{
		{Name: "get_weather", Description: "Get the current weather for a given location. Argument: location (string)."},
	}
	comp, err := model.Complete(callCtx(t), msgs, tools)
	if err != nil {
		t.Fatalf("Complete() with tools failed: %v", err)
	}
	if len(comp.ToolCalls) == 0 {
		t.Fatalf("want a tool call, got none; message was %q", comp.Message.Content)
	}
	tc := comp.ToolCalls[0]
	if tc.Name != "get_weather" {
		t.Errorf("want tool %q, got %q", "get_weather", tc.Name)
	}
	if !json.Valid(tc.Args) {
		t.Errorf("want valid-JSON args, got %q", tc.Args)
	}
	if len(tc.Args) == 0 || string(tc.Args) == "{}" {
		t.Errorf("want populated tool args (e.g. a location), got %q", tc.Args)
	}
}

func TestAnthropicModel_EmitsProposeArgsThatDecodeIntoProposalSet(t *testing.T) {
	model := newModel(t)

	msgs := []clank.Message{{Role: "user", Content: strings.Join([]string{
		"You are clank, a reliability reasoning plane.",
		"Signal: latency_p99 on the checkout service is degraded ~80%, blast radius ~50%.",
		"You have already gathered evidence; the downstream payments-db is CPU-saturated.",
		"Emit your final answer by calling the `propose` tool.",
		"The ONLY action you may propose is the catalog contract `throttle-non-critical-paths`.",
	}, " ")}}

	// The production propose spec — its schema is generated from proposeInput,
	// so this test exercises the real autonomy-boundary contract, not a lookalike.
	tools := []clank.ToolSpec{clank.ProposeToolSpec()}

	comp, err := model.Complete(callCtx(t), msgs, tools)
	if err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	if len(comp.ToolCalls) == 0 {
		t.Fatalf("want a propose tool call, got none; message was %q", comp.Message.Content)
	}
	tc := comp.ToolCalls[0]
	if tc.Name != "propose" {
		t.Fatalf("want tool %q, got %q", "propose", tc.Name)
	}

	var set clank.ProposalSet
	if err := json.Unmarshal(tc.Args, &set); err != nil {
		t.Fatalf("propose args do not decode into ProposalSet: %v\nargs: %s", err, tc.Args)
	}
	if set.FailureClass == "" {
		t.Errorf("want a FailureClass, got empty; args: %s", tc.Args)
	}
	if len(set.Proposals) == 0 {
		t.Fatalf("want at least one proposal, got none; args: %s", tc.Args)
	}
	if got := set.Proposals[0].ContractRef; got != "throttle-non-critical-paths" {
		t.Errorf("want fenced contractRef %q, got %q", "throttle-non-critical-paths", got)
	}
}

func TestAnthropicModel_ContinuesConversationAfterToolResult(t *testing.T) {
	model := newModel(t)

	msgs := []clank.Message{
		{Role: "user", Content: "Investigate the latency on payments-db using the metrics tool."},
		{Role: "assistant", Content: "I'll check the current metrics for payments-db."},
		{Role: "tool", Content: "metrics digest: payments-db CPU is pinned at 99% (codeword: FLAMINGO)."},
		{Role: "user", Content: "Based only on that metrics digest, what is the codeword? Reply with just the word."},
	}
	comp, err := model.Complete(callCtx(t), msgs, nil)
	if err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	if !strings.Contains(strings.ToUpper(comp.Message.Content), "FLAMINGO") {
		t.Errorf("model did not carry the tool-result fact forward; want %q in %q", "FLAMINGO", comp.Message.Content)
	}
}

func TestAnthropicModel_DeclinesToolWhenPromptDoesNotNeedIt(t *testing.T) {
	model := newModel(t)

	msgs := []clank.Message{
		{Role: "user", Content: "Reply with exactly the word ACK. Do not call any tools."},
	}
	tools := []clank.ToolSpec{
		{Name: "get_weather", Description: "Get the current weather for a given location."},
	}
	comp, err := model.Complete(callCtx(t), msgs, tools)
	if err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	if len(comp.ToolCalls) != 0 {
		t.Errorf("prompt needed no tool, want 0 tool calls, got %d (%+v)", len(comp.ToolCalls), comp.ToolCalls)
	}
	if !strings.Contains(comp.Message.Content, "ACK") {
		t.Errorf("want %q in content, got %q", "ACK", comp.Message.Content)
	}
}

func TestAnthropicModel_ReturnsErrorOnCancelledContext(t *testing.T) {
	model := newModel(t)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := model.Complete(cancelled, []clank.Message{
		{Role: "user", Content: "hello"},
	}, nil)
	if err == nil {
		t.Fatal("want an error from a cancelled context, got nil")
	}
}
