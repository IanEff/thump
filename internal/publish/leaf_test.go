package publish_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestPublishIsALeafPackage pins that internal/publish — the one port every
// beat imports — imports none of them back. A rattle/clank/hiss/thump import
// here is the exact cycle this port exists to avoid.
func TestPublishIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		"bytes", "context", "errors", "encoding/json", "fmt", "io", "io/fs",
		"os", "path/filepath", "strconv", "strings", "sync", "time",
		"sigs.k8s.io/yaml",
		"github.com/ianeff/thump/internal/wire",
		"github.com/nats-io/nats.go",
		"github.com/nats-io/nats.go/jetstream",
		"go.opentelemetry.io/otel/propagation",
	)
}
