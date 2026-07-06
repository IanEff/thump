package thump_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestThumpCannotReachInfrastructure(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		// stdlib, none of it load-bearing for mischief
		`"context"`: true, `"errors"`: true, `"flag"`: true, `"fmt"`: true,
		`"io"`: true, `"log/slog"`: true, `"os"`: true, `"os/signal"`: true,
		`"path/filepath"`: true, `"sync"`: true, `"syscall"`: true, `"time"`: true,
		// the wire codec (hiss writes with it; we read with it)
		`"sigs.k8s.io/yaml"`: true,
		// the leaves — vocabulary only, no behavior that touches the world
		`"github.com/ianeff/thump/api/v1/decision"`:   true,
		`"github.com/ianeff/thump/api/v1/proposal"`:   true,
		`"github.com/ianeff/thump/internal/contract"`: true,
		`"github.com/ianeff/thump/api/v1/outcome"`:    true,
		// the publish port — a local dir-writer today, same risk profile as the
		// writeAtomic thump used to inline; revisit at Stage 3, when this
		// package grows a live JetStream implementation.
		`"github.com/ianeff/thump/internal/publish"`: true,
		// Stage 3b: the live JetStream implementation arrived. NATS is the
		// beat-to-beat transport (same risk profile as the dir glob it sits
		// beside), not infrastructure thump acts on — I-10 is about Exec
		// staying dry-run, not about how a Governed decision reaches thump.
		`"github.com/ianeff/thump/internal/broker"`: true,
		`"github.com/nats-io/nats.go"`:              true,
		`"github.com/nats-io/nats.go/jetstream"`:    true,
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
			if !allowed[imp.Path.Value] {
				t.Errorf("%s imports %s — v1 thump is dry-run BY CONSTRUCTION (I-10); growing this allowlist is a design review, not a convenience",
					name, imp.Path.Value)
			}
		}
	}
}
