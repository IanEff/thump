package clank_test

import (
	"testing"

	"github.com/ianeff/thump/internal/clank"
)

func TestNewBrokerEngine_WiresMetricsTool(t *testing.T) {
	// 1. Arrange: provide the tools map we expect Main to build
	tools := map[string]clank.Tool{
		"metrics": &clank.MetricsTool{},
	}

	// 2. Act: construct the broker engine exactly as runBroker does
	// We can pass nil for the heavy dependencies we don't care about here.
	eng := clank.NewBrokerEngineForTest(
		nil, // model
		nil, // intake
		nil, // store
		tools,
		nil, // pub
		clank.NewMemProposalLog(),
		clank.NewCaseBase(),
	)

	// 3. Assert: the regression pin. If this fails, production loops forever with no evidence.
	if eng.Tools["metrics"] == nil {
		t.Error("expected broker engine to have the 'metrics' tool wired, but it was nil")
	}
}
