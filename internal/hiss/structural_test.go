package hiss_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// controllerFile is the one file in internal/hiss permitted to reach
// Kubernetes: the ApprovalRequest watch loop. W2 gives hiss a client, but
// every other file stays exactly as reachable as it was before — no
// typed or dynamic Kubernetes client anywhere else in the package.
const controllerFile = "approvalrequest_controller.go"

// TestHissClientGoConfinedToController is internal/hiss's own copy of
// internal/thump/structural_test.go:12's shape: a closed allowlist, denied
// by default, so a new import is a visible test failure rather than a
// silent widening. It adds one rule thump's tripwire doesn't need — an
// import path rooted at k8s.io/ is only legal inside controllerFile,
// wherever it appears in the package. Widening either list is a design
// review, not a convenience.
func TestHissClientGoConfinedToController(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		// stdlib
		`"context"`: true, `"crypto/sha256"`: true, `"crypto/tls"`: true, `"encoding/hex"`: true,
		`"encoding/json"`: true, `"errors"`: true, `"fmt"`: true,
		`"io"`: true, `"log/slog"`: true, `"os"`: true, `"path/filepath"`: true,
		`"sync"`: true, `"time"`: true,
		// the wire codec
		`"sigs.k8s.io/yaml"`: true,
		// tracing and the goroutine-lifecycle kit
		`"go.opentelemetry.io/otel/trace"`:       true,
		`"go.opentelemetry.io/otel/trace/noop"`:  true,
		`"golang.org/x/sync/errgroup"`:           true,
		`"github.com/nats-io/nats.go/jetstream"`: true,
		// boundary vocabulary
		`"github.com/ianeff/thump/api/v1/approval"`: true,
		`"github.com/ianeff/thump/api/v1/decision"`: true,
		`"github.com/ianeff/thump/api/v1/proposal"`: true,
		// runtime kit and transports
		`"github.com/ianeff/thump/internal/beat"`:        true,
		`"github.com/ianeff/thump/internal/broker"`:      true,
		`"github.com/ianeff/thump/internal/config"`:      true,
		`"github.com/ianeff/thump/internal/health"`:      true,
		`"github.com/ianeff/thump/internal/ledger"`:      true,
		`"github.com/ianeff/thump/internal/objectstore"`: true,
		`"github.com/ianeff/thump/internal/poll"`:        true,
		`"github.com/ianeff/thump/internal/publish"`:     true,
		`"github.com/ianeff/thump/internal/sealbox"`:     true,
		`"github.com/ianeff/thump/internal/tlsx"`:        true,
		`"github.com/ianeff/thump/internal/wire"`:        true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path := imp.Path.Value
			if strings.HasPrefix(path, `"k8s.io/`) {
				if name != controllerFile {
					t.Errorf("%s imports %s — Kubernetes clients stay inside %s, hiss's one ApprovalRequest watch loop; widening this beyond that file is a design review, not a convenience",
						name, path, controllerFile)
				}
				continue
			}
			if !allowed[path] {
				t.Errorf("%s imports %s — not on hiss's allowlist; widening it is a design review, not a convenience",
					name, path)
			}
		}
	}
}
