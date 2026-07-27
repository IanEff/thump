package broker

import (
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/ianeff/thump/internal/tlsx"
)

// DialOptionsForTest resolves the options Connect dials under into the
// nats.Options they produce, so a test can read the retry policy directly
// rather than inferring it from how long a disconnected client keeps trying.
func DialOptionsForTest(t *testing.T, hooks Hooks) nats.Options {
	t.Helper()
	var closing atomic.Bool
	opts, err := dialOptions(tlsx.Config{}, hooks, &closing)
	if err != nil {
		t.Fatal("dial options:", err)
	}
	var resolved nats.Options
	for _, opt := range opts {
		if err := opt(&resolved); err != nil {
			t.Fatal("apply option:", err)
		}
	}
	return resolved
}
