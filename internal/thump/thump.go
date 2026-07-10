// Package thump is the Act beat: it renders (and, later, executes) a
// governed decision.Decision. Actuator.Render turns one approval into an
// Order, invented from nothing more than the Decision, the Set's recommended
// Candidate, and the ActionContract catalog; Executor then performs it. v1
// is structurally dry-run — DryRun is the only Executor, and an
// import-allowlist test on this package proves no code path here can reach
// os/exec, net, or a Kubernetes client, rather than merely trusting a flag
// to keep it that way.
package thump

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
	"go.opentelemetry.io/otel/trace"
)

// Main is thump's process entry point: run either the NATS branch (consume
// thump.decisions, render + dry-run-execute, publish thump.orders and
// thump.outcomes) or the directory-poll fallback (THUMP_INBOX/THUMP_OUTBOX)
// depending on whether a NATS URL is configured. It returns a process exit
// code rather than calling os.Exit, so the whole startup path stays
// testable.
func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	lc, code, exit := beat.Start("thump", args, stdout, stderr, beat.Version{Version: version, Commit: commit, Date: date})
	if exit {
		return code
	}
	defer lc.Stop()
	ctx := lc.Ctx

	tracer, shutdownTracer, err := beat.Tracer(ctx, "thump")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v\n", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	if lc.NATSURL != "" {
		return runBroker(ctx, lc.NATSURL, tracer, stderr)
	}

	// offline path: the dir-glob Transport is now the keyless fake the seam
	// tests exercise — broker mode above is how this actually runs. THUMP_INBOX/
	// OUTBOX are this path's env, not the process's — checked here, not above,
	// so broker mode never has to satisfy them (mirrors rattle.go's NATS_URL-
	// first branch).
	inbox := os.Getenv("THUMP_INBOX")
	if inbox == "" {
		_, _ = fmt.Fprintln(stderr, "THUMP_INBOX is required")
		return 1
	}
	outbox := os.Getenv("THUMP_OUTBOX")
	if outbox == "" {
		_, _ = fmt.Fprintln(stderr, "THUMP_OUTBOX is required")
		return 1
	}

	tr := &Transport{
		Inbox: inbox,
		OrderPub: &publish.DirPublisher[Order]{
			Dir:  filepath.Join(outbox, "orders"),
			Name: func(o Order) string { return o.SignalRef },
		},
		OutcomePub: &publish.DirPublisher[outcome.Outcome]{
			Dir:  filepath.Join(outbox, "outcomes"),
			Name: func(o outcome.Outcome) string { return o.SignalRef },
		},
		Catalog: contract.Default(),
		Log:     NewOutcomeLog(),
		Exec:    DryRun{},
		Tracer:  tracer,
	}
	beat.PollLoop(ctx, beat.PollConfig{Interval: 5 * time.Second}, tr.Tick)
	return 0
}

// runBroker is thump's NATS branch: consume thump.decisions, render +
// dry-run-execute, publish thump.orders + thump.outcomes. thump.orders has no
// consumer (DurableFor("thump.orders") == "") — publishing it anyway is
// fine, WAL-only the day it stops being fine, per Ian's call.
func runBroker(ctx context.Context, natsURL string, tracer trace.Tracer, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	walDir := os.Getenv("WAL_DIR")
	orderPub, closeOrders, err := beat.NewWALPublisher[Order](js, walDir, "thump", "thump.orders")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeOrders(ctx) }()
	outcomePub, closeOutcomes, err := beat.NewWALPublisher[outcome.Outcome](js, walDir, "thump", "thump.outcomes")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeOutcomes(ctx) }()

	tr := &Transport{
		OrderPub:   orderPub,
		OutcomePub: outcomePub,
		Catalog:    contract.Default(),
		Log:        NewOutcomeLog(),
		Exec:       DryRun{},
		Tracer:     tracer,
	}

	return beat.ExitOnError(ctx, beat.RunConsumer[decision.Governed](ctx, js, "thump.decisions", tr.handle))
}
