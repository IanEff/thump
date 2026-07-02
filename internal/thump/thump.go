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
	"syscall"
	"time"

	"github.com/ianeff/clank/internal/contract"
	"github.com/ianeff/clank/internal/proposal"
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

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting thump", "version", version, "commit", commit, "date", date)

	tr := &Transport{
		Inbox:   inbox,
		Outbox:  outbox,
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

// defaultCatalog is v1's compiled-in authored catalog (Precondition.OK is a
// Go func, so it can't ride YAML — PARKED until a precondition DSL exists).
// thump and clank read the same authored actions; until clank's Main wires
// an engine, thump is the only binary that needs one at runtime.
func defaultCatalog() *contract.StaticCatalog {
	return contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		Action: contract.ActionSpec{
			Description:     "Throttle non-critical request paths at the ingress",
			ScopeParameters: map[string]contract.Range{"throttle_pct": {Min: 10, Max: 60, Default: 25}},
		},
		Reversal:        contract.Reversal{Method: "unthrottle", Fallback: "page-oncall"},
		SuccessCriteria: contract.SuccessCriteria{Metric: "latency_p99", Target: "p99 < 250ms", Window: 10 * time.Minute},
	}})
}
