// Package modelsel resolves the -model flag probe and rca both expose into a
// real reason.Model — the one place that knows the three names production
// tooling accepts, so the two CLIs can't drift into naming a fourth.
package modelsel

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/genai"

	"github.com/ianeff/thump/internal/anthropic"
	"github.com/ianeff/thump/internal/gemini"
	"github.com/ianeff/thump/internal/reason"
)

// RequestTimeout bounds every model call an offline instrument makes — a
// sweep firing dozens of draws needs a hung request to fail fast, not stall
// the run.
const RequestTimeout = 120 * time.Second

// For builds the reason.Model behind name — "haiku" (production's own
// choice), "sonnet", or "gemini-low". A non-empty skip names the environment
// variable a required key was missing from, signaling the caller's clean-skip
// path rather than an error it should report and exit non-zero for.
func For(ctx context.Context, name string) (model reason.Model, skip string, err error) {
	switch name {
	case "haiku":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, "ANTHROPIC_API_KEY", nil
		}
		return anthropic.NewModel(key, anthropic.ModelClaudeHaiku4_5, RequestTimeout), "", nil
	case "sonnet":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, "ANTHROPIC_API_KEY", nil
		}
		return anthropic.NewModel(key, anthropic.ModelClaudeSonnet5, RequestTimeout), "", nil
	case "gemini-low":
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" {
			return nil, "GEMINI_API_KEY", nil
		}
		m, err := gemini.NewModel(ctx, key, gemini.ModelGemini3_5FlashLite, genai.ThinkingLevelLow)
		if err != nil {
			return nil, "", fmt.Errorf("build gemini model: %w", err)
		}
		return m, "", nil
	default:
		return nil, "", fmt.Errorf("unknown -model %q: want haiku, sonnet, or gemini-low", name)
	}
}
