package pipeline

import (
	"context"

	"github.com/ianeff/thump/internal/reason"
)

// RunWithModelForTest allows tests to drive Run using a custom reason.Model
// seam and injected tools rather than calling live Anthropic APIs.
func RunWithModelForTest(ctx context.Context, detectionFile, profileDir string, model reason.Model, tools map[string]reason.Tool) (Result, error) {
	return runWithModelAndTools(ctx, detectionFile, profileDir, model, tools)
}
