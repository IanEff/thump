//go:build eval
// +build eval

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

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/anthropic"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/reason"
)

func newModel(t *testing.T) reason.Model {
	t.Helper()
	key := apiKey(t)
	if key == "" {
		t.Skip("skipping integration test: ANTHROPIC_API_KEY not set (and no .env)")
	}
	return anthropic.NewModel(key, 120*time.Second)
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

func TestAnthropicModel_EmitsProposeArgsThatDecodeIntoProposalSet(t *testing.T) {
	model := newModel(t)

	msgs := []reason.Message{{Role: "user", Content: strings.Join([]string{
		"You are clank, a reliability reasoning plane.",
		"Signal: latency_p99 on the checkout service is degraded ~80%, blast radius ~50%.",
		"You have already gathered evidence; the downstream payments-db is CPU-saturated.",
		"Emit your final answer by calling the `propose` tool.",
		"The ONLY action you may propose is the catalog contract `throttle-non-critical-paths`.",
	}, " ")}}

	// The production propose spec — its schema is generated from proposeInput,
	// so this test exercises the real autonomy-boundary contract, not a lookalike.
	tools := []reason.ToolSpec{clank.ProposeToolSpec()}

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

	var set proposal.Set
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

func TestAnthropicModel_AnswersEachOfATurnsParallelCallsDistinctly(t *testing.T) {
	model := newModel(t)

	msgs := []reason.Message{
		{Role: "user", Content: "Check burn and errors with the metrics tool, then report both."},
		{Role: "assistant", Content: "Checking both.", ToolCalls: []reason.ToolCall{
			{ID: "toolu_burn", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			{ID: "toolu_errors", Name: "metrics", Args: json.RawMessage(`{"q":"errors"}`)},
		}},
		{Role: "tool", ToolResults: []reason.ToolResult{
			{CallID: "toolu_burn", Name: "metrics", Digest: "codeword FLAMINGO"},
			{CallID: "toolu_errors", Name: "metrics", Digest: "codeword WALRUS"},
		}},
		{Role: "user", Content: `Reply with exactly: burn=<codeword> errors=<codeword>`},
	}

	comp, err := model.Complete(callCtx(t), msgs, []reason.ToolSpec{{
		Name: "metrics", Description: "read-only telemetry query",
	}})
	if err != nil {
		t.Fatal(err)
	}

	got := strings.ToUpper(comp.Message.Content)
	if !strings.Contains(got, "BURN=FLAMINGO") {
		t.Errorf("the burn call's answer was not attributed to it; want %q in %q", "burn=FLAMINGO", comp.Message.Content)
	}
	if !strings.Contains(got, "ERRORS=WALRUS") {
		t.Errorf("the errors call's answer was not attributed to it; want %q in %q", "errors=WALRUS", comp.Message.Content)
	}
}
