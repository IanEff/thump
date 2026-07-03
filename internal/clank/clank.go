package clank

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting clank", "version", version, "commit", commit, "date", date)

	// TODO(straggler A3/A4): wire newLoop + Transport + ReturnEdge tickers here.
	// Parked until Claim A1 (Transport) is green on its own.
	<-ctx.Done()

	slog.Info("shutting down")
	return 0
}

type loop struct {
	Engine       *Engine
	ReturnEdge   *ReturnEdge
	Cases        *CaseBase
	OutcomeInbox string
}

func newLoop(inbox, outbox, outcomes string, model Model, tools map[string]Tool, intake *Intake, cat *StaticCatalog) *loop {
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
