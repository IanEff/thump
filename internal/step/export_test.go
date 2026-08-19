package step

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/reason"
)

// RunClankWithModelForTest allows tests to drive RunClank using a custom
// reason.Model seam rather than calling live Anthropic APIs.
func RunClankWithModelForTest(ctx context.Context, detectionFile, profileDir string, model reason.Model) (proposal.Set, error) {
	return runClank(ctx, detectionFile, profileDir, model)
}

// RunClankWithModelAndToolsForTest allows tests to drive RunClank using a
// custom reason.Model seam and injected tools.
func RunClankWithModelAndToolsForTest(ctx context.Context, detectionFile, profileDir string, model reason.Model, tools map[string]reason.Tool) (proposal.Set, error) {
	return runClankWithTools(ctx, detectionFile, profileDir, model, tools)
}
