package thump_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThumpCannotReachInfrastructure(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		// stdlib, none of it load-bearing for mischief
		`"context"`: true, `"crypto/tls"`: true, `"errors"`: true, `"flag"`: true, `"fmt"`: true,
		`"io"`: true, `"log/slog"`: true, `"os"`: true, `"os/signal"`: true,
		`"path/filepath"`: true, `"sync"`: true, `"syscall"`: true, `"time"`: true,
		// the wire codec (hiss writes with it; we read with it)
		`"sigs.k8s.io/yaml"`: true,
		// the leaves — vocabulary only, no behavior that touches the world
		`"github.com/ianeff/thump/api/v1/decision"`:   true,
		`"github.com/ianeff/thump/api/v1/proposal"`:   true,
		`"github.com/ianeff/thump/internal/contract"`: true,
		// C1: typed env loading. Its own leaftest pins it to errors/fmt/net/url/os
		// only — reads and validates strings, touches nothing outside the
		// process. Same risk profile as contract above, not a widening.
		`"github.com/ianeff/thump/internal/config"`: true,
		`"github.com/ianeff/thump/api/v1/outcome"`:  true,
		// the publish port — a local dir-writer, same risk profile as the
		// writeAtomic thump used to inline before this package existed.
		`"github.com/ianeff/thump/internal/publish"`: true,
		// The beat-to-beat transport, same risk profile as the dir glob it
		// sits beside — not infrastructure thump acts on.
		`"github.com/ianeff/thump/internal/broker"`: true,
		// The one infra-reaching import: Live's injected ActionRunner. I-10
		// permits this behind GatedExecutor's kill-switch check, not by
		// keeping Exec dry-run — that seal was deliberately broken once the
		// governance path was real (docs/invariants.md I-10).
		`"github.com/ianeff/thump/internal/actuate"`: true,
		// Phase F Wave D: the automatic-undo probe. Reads Prometheus over
		// net/http, same as actuate reaches os/exec — expressed in primitives
		// (metric, target strings), never Order, so this stays a one-directional
		// import with no cycle back through thump. See PrometheusConverger.
		`"github.com/ianeff/thump/internal/converge"`: true,
		// The one place an outbound HTTP client is built (net/http + time
		// only, stdlib per its own leaf tripwire). thump.go injects it into
		// converge.Prober so that call is bounded too — building a client
		// isn't a new capability the way exec or a live actuator would be.
		`"github.com/ianeff/thump/internal/httpx"`: true,
		`"github.com/nats-io/nats.go"`:             true,
		`"github.com/nats-io/nats.go/jetstream"`:   true,
		// R6: the client-side half of thump's own mTLS leg to NATS —
		// constructs a *tls.Config from three file paths, presents this
		// beat's leaf, and verifies the broker's. No new reach: it's the
		// credential for the transport already allowed above, not a new one.
		`"github.com/ianeff/thump/internal/tlsx"`: true,
		// the runtime kit: process lifecycle + the same broker/publish
		// transports already allowed above. Its own leaf tripwire forbids it
		// from ever importing a beat package (rattle/clank/hiss/thump), not
		// from importing infrastructure-reaching SDKs — it already carries the
		// OTel exporter and now the S3 client. What keeps that safe for thump
		// is that neither SDK's types cross this import boundary: beat hands
		// back trace.Tracer and publish.SegmentSink, both interfaces thump
		// already trusts, never the concrete otlptracegrpc/aws-sdk-go-v2 types.
		`"github.com/ianeff/thump/internal/beat"`: true,
		// C1a: the /healthz + /readyz surface beat.Metrics used to build
		// inline. net/http + sync/atomic only per its own leaf tripwire — a
		// liveness flag and an HTTP handler, not a new capability split out
		// of beat.
		`"github.com/ianeff/thump/internal/health"`: true,
		// C1b: the offline dir-poll ticker beat.PollLoop used to be. Stdlib
		// only per its own leaf tripwire — a select loop over a
		// time.Ticker/time.Timer, not a new capability.
		`"github.com/ianeff/thump/internal/poll"`: true,
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
		// R8: AES-256-GCM sealing for WAL segments shipped to the bucket —
		// crypto/aes, crypto/cipher, crypto/rand, fmt only per its own leaf
		// tripwire. Same risk profile as ledger above: a data transform with
		// no reach outside the process, not a new capability.
		`"github.com/ianeff/thump/internal/sealbox"`: true,
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
				t.Errorf("%s imports %s — infra-reaching imports stay inside internal/actuate, gated by GatedExecutor and the kill switch (I-10); growing this allowlist is a design review, not a convenience",
					name, imp.Path.Value)
			}
		}
	}
}

func TestNotifierSDKNeverReachesCoreBeats(t *testing.T) {
	t.Parallel()
	const slackModule = `"github.com/slack-go/slack"`
	for _, dir := range []string{".", "../hiss"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				if imp.Path.Value == slackModule {
					t.Errorf("%s/%s imports the Slack SDK directly — it belongs in internal/notify/slack, behind Notifier", dir, name)
				}
			}
		}
	}
}
