package integration_test

import (
	"context"
	"testing"

	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/reason"
)

// TestAnthropicModel_ReturnsErrorOnCancelledContext belongs in the default
// build, not eval: a cancelled context never reaches the network, so this
// costs no API call and needs no key. The key is a dummy — irrelevant to the
// behavior under test.
func TestAnthropicModel_ReturnsErrorOnCancelledContext(t *testing.T) {
	model := clank.NewAnthropicModel("dummy key")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := model.Complete(cancelled, []reason.Message{
		{Role: "user", Content: "hello"},
	}, nil)
	if err == nil {
		t.Fatal("want an error from a cancelled context, got nil")
	}
}
