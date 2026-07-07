package thump

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
)

func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	fs := flag.NewFlagSet("thump", flag.ExitOnError)
	fs.SetOutput(stderr)

	printVersion := fs.Bool("version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "failed to parse flags: %v\n", err)
		return 1
	}

	if *printVersion {
		_, _ = fmt.Fprintf(stdout, "thump %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting thump", "version", version, "commit", commit, "date", date)

	if natsURL := os.Getenv("NATS_URL"); natsURL != "" {
		return runBroker(ctx, natsURL, stderr)
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
		Catalog: defaultCatalog(),
		Log:     NewOutcomeLog(),
		Exec:    DryRun{},
	}
	ticker := time.NewTicker(5 * time.Second) // TODO: or whatever
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return 0
		case <-ticker.C:
			if err := tr.Tick(ctx); err != nil {
				slog.Error("tick failed", "err", err)
			}
		}
	}
}

// runBroker is thump's NATS branch: consume thump.decisions, render +
// dry-run-execute, publish thump.orders + thump.outcomes — mirrors
// clank.runBroker's and hiss.runBroker's shape. thump.orders has no
// consumer (DurableFor("thump.orders") == "") — publishing it anyway is
// fine, WAL-only the day it stops being fine, per Ian's call.
func runBroker(ctx context.Context, natsURL string, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	walDir := os.Getenv("WAL_DIR")
	if walDir == "" {
		_, _ = fmt.Fprintln(stderr, "WAL_DIR is required with NATS_URL")
		return 1
	}
	wOrders := &publish.WAL{Dir: walDir, Beat: "thump", Subject: "thump.orders"}
	defer func() { _ = wOrders.Close(ctx) }()
	wOutcomes := &publish.WAL{Dir: walDir, Beat: "thump", Subject: "thump.outcomes"}
	defer func() { _ = wOutcomes.Close(ctx) }()

	tr := &Transport{
		OrderPub:   &publish.WALPublisher[Order]{WAL: wOrders, Next: publish.NewJetPublisher[Order](js)},
		OutcomePub: &publish.WALPublisher[outcome.Outcome]{WAL: wOutcomes, Next: publish.NewJetPublisher[outcome.Outcome](js)},
		Catalog:    defaultCatalog(),
		Log:        NewOutcomeLog(),
		Exec:       DryRun{},
	}

	sub := broker.NewJetSubscriber[decision.Governed](js)
	if err := sub.Run(ctx, "thump.decisions", tr.handle); err != nil && ctx.Err() == nil {
		slog.Error("broker run failed", "err", err)
		return 1
	}
	return 0
}

// defaultCatalog is v1's compiled-in authored catalog (Precondition.OK is a
// Go func, so it can't ride YAML — PARKED until a precondition DSL exists).
// thump and clank read the same authored actions; until clank's Main wires
// an engine, thump is the only binary that needs one at runtime.
func defaultCatalog() *contract.StaticCatalog {
	return contract.NewStaticCatalog([]contract.ActionContract{
		{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Action: contract.ActionSpec{
				Description:     "Throttle non-critical request paths at the ingress",
				ScopeParameters: map[string]contract.Range{"throttle_pct": {Min: 10, Max: 60, Default: 25}},
			},
			Reversal:        contract.Reversal{Method: "unthrottle", Fallback: "page-oncall"},
			SuccessCriteria: contract.SuccessCriteria{Metric: "latency_p99", Target: "p99 < 250ms", Window: 10 * time.Minute},
		},
		{
			Name: "hold-rebalance",
			ApplicableFailureClasses: []proposal.FailureClass{
				proposal.ClassResourceExhaustion,
				proposal.ClassUnknown,
			},
			ApplicableTiers: []string{"tier-1"},
			Action: contract.ActionSpec{
				Description: "Hold Ceph recovery/rebalancing (osd set noout) while a " +
					"node is transiently out, so the cluster doesn't thrash; reversible.",
				ScopeParameters: map[string]contract.Range{
					"hold_minutes": {Min: 5, Max: 60, Default: 15},
				},
			},
			Reversal: contract.Reversal{
				Method:   "release-rebalance",
				Fallback: "page-oncall",
			},
			SuccessCriteria: contract.SuccessCriteria{
				Metric: "ceph_health",
				Target: "HEALTH_OK",
				Window: 10 * time.Minute,
			},
		},
	})
}
