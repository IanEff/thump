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
		// C1: typed env loading. Its own leaftest pins it to errors/fmt/os
		// only — reads and validates strings, touches nothing outside the
		// process. Same risk profile as contract above, not a widening.
		`"github.com/ianeff/thump/internal/config"`: true,
		`"github.com/ianeff/thump/api/v1/outcome"`:  true,
		// the publish port — a local dir-writer today, same risk profile as the
		// writeAtomic thump used to inline; revisit at Stage 3, when this
		// package grows a live JetStream implementation.
		`"github.com/ianeff/thump/internal/publish"`: true,
		// Stage 3b: the live JetStream implementation arrived. NATS is the
		// beat-to-beat transport (same risk profile as the dir glob it sits
		// beside), not infrastructure thump acts on — I-10 is about Exec
		// staying dry-run, not about how a Governed decision reaches thump.
		`"github.com/ianeff/thump/internal/broker"`:  true,
		`"github.com/ianeff/thump/internal/actuate"`: true,
		`"github.com/nats-io/nats.go"`:               true,
		`"github.com/nats-io/nats.go/jetstream"`:     true,
		// the runtime kit: process lifecycle + the same broker/publish
		// transports already allowed above. Its own leaf tripwire forbids it
		// from ever importing a beat package (rattle/clank/hiss/thump), not
		// from importing infrastructure-reaching SDKs — it already carries the
		// OTel exporter and now the S3 client. What keeps that safe for thump
		// is that neither SDK's types cross this import boundary: beat hands
		// back trace.Tracer and publish.SegmentSink, both interfaces thump
		// already trusts, never the concrete otlptracegrpc/aws-sdk-go-v2 types.
		`"github.com/ianeff/thump/internal/beat"`: true,
		// pure goroutine-lifecycle plumbing (WithContext + Go + Wait) — no net,
		// no os/exec, no client. runBroker uses it to run the WAL shipper(s)
		// alongside the consumer loop, the same composition clank/broker.go
		// already uses for two subscribers.
		`"golang.org/x/sync/errgroup"`: true,
		// the in-memory outcome ledger: sync + time only, a data structure that
		// touches nothing outside the process. Where OutcomeLog's append/query
		// lives, not a new capability.
		`"github.com/ianeff/thump/internal/ledger"`: true,
		// B1: the bare OTel trace API — Tracer/Span interfaces and value types
		// only (TraceID, SpanID). `go list -deps` shows zero net anywhere under
		// it; the actual network-reaching half, the SDK's exporters, is never
		// imported here — Main constructs and injects the real Tracer, same
		// pattern as every other seam on Transport.
		`"go.opentelemetry.io/otel/trace"`:      true,
		`"go.opentelemetry.io/otel/trace/noop"`: true,
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
