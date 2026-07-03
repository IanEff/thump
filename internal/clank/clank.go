package clank

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	ossignal "os/signal"
	"syscall"
	"time"

	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/proposal"
	"github.com/ianeff/thump/internal/signal"
)

func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	fs := flag.NewFlagSet("clank", flag.ContinueOnError)
	fs.SetOutput(stdout)

	printVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "failed to parse flags: %v\n", err)
		return 1
	}

	if *printVersion {
		_, _ = fmt.Fprintf(stdout, "clank %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting clank", "version", version, "commit", commit, "date", date)

	inbox := os.Getenv("CLANK_INBOX") // rattle's detection output dir
	if inbox == "" {
		_, _ = fmt.Fprintln(stderr, "CLANK_INBOX is required")
		return 1
	}
	outbox := os.Getenv("CLANK_OUTBOX") // hiss's inbox
	if outbox == "" {
		_, _ = fmt.Fprintln(stderr, "CLANK_OUTBOX is required")
		return 1
	}
	outcomes := os.Getenv("CLANK_OUTCOMES") // thump's outbox (the return edge's inbox)
	if outcomes == "" {
		_, _ = fmt.Fprintln(stderr, "CLANK_OUTCOMES is required")
		return 1
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		_, _ = fmt.Fprintln(stderr, "ANTHROPIC_API_KEY is required")
		return 1
	}
	model := NewAnthropicModel(apiKey)
	intake := NewIntake(noopTopology{}, noopChange{})

	l := newLoop(inbox, outbox, outcomes, model, nil, intake, defaultCatalog())
	tr := &Transport{Inbox: inbox, Engine: l.Engine}

	runLoop(ctx, tr, l.ReturnEdge)
	return 0
}

// runLoop is Main's ticker-driven body, pulled into its own function so it
// takes a context directly — a test can cancel that context and observe a
// prompt return, with no OS signals involved (Main's ctx comes from
// NotifyContext; this one doesn't have to).
func runLoop(ctx context.Context, tr *Transport, re *ReturnEdge) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return
		case <-ticker.C:
			if err := tr.Tick(ctx); err != nil {
				slog.Error("reason tick failed", "err", err)
			}
			if err := re.Tick(ctx); err != nil {
				slog.Error("learn tick failed", "err", err)
			}
		}
	}
}

// noopTopology and noopChange are placeholders for clank's real telemetry /
// change backends (Prometheus, ArgoCD) — still deferred (see the clank
// implementation guide's "one honest open item"). They let Main's loop
// structurally run today; the SAO it assembles just carries no live
// topology/change context until the real sources land.
type noopTopology struct{}

func (noopTopology) Topology(context.Context, signal.Detection) (TopologySnapshot, error) {
	return TopologySnapshot{}, nil
}

type noopChange struct{}

func (noopChange) Changes(context.Context, signal.Detection) (ChangeSnapshot, error) {
	return ChangeSnapshot{}, nil
}

// defaultCatalog is clank's copy of thump's compiled-in authored catalog —
// the same action, both binaries. thump's comment predates this: "until
// clank's Main wires an engine, thump is the only binary that needs one at
// runtime." Now both do.
func defaultCatalog() *StaticCatalog {
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

type loop struct {
	Engine       *Engine
	ReturnEdge   *ReturnEdge
	Cases        *CaseBase
	OutcomeInbox string
}

func newLoop(_, outbox, outcomes string, model Model, tools map[string]Tool, intake *Intake, cat *StaticCatalog) *loop {
	ledger := NewMemProposalLog() // ONE ledger
	cases := NewCaseBase()        // ONE case base
	eng := &Engine{
		Intake:       intake,
		Model:        model,
		Tools:        tools,
		Catalog:      cat,
		Ranker:       NewRanker(),
		Store:        NewMemStore(),
		Scorer:       &CausalScorerImpl{Prior: cases}, // scorer reads THIS case base
		DedupeWindow: time.Hour,
		Ledger:       ledger, // engine records into THIS ledger
		Sink:         &DirSink{Dir: outbox},
		Gate:         ReadinessGate{},
		MaxSteps:     8,
	}
	re := &ReturnEdge{
		Inbox: outcomes, // thump's outbox — NOT outbox, which is hiss's inbox
		Click: Click{Ledger: ledger, Cases: cases},
	}
	return &loop{Engine: eng, ReturnEdge: re, Cases: cases, OutcomeInbox: outcomes}
}
