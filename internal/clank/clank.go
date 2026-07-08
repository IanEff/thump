package clank

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	ossignal "os/signal"
	"syscall"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/whir"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	transcripts := os.Getenv("CLANK_TRANSCRIPTS") // thump's transcript output dir
	if transcripts == "" {
		slog.Info("CLANK_TRANSCRIPTS not set — turns held in memory, not persisted")
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		_, _ = fmt.Fprintln(stderr, "ANTHROPIC_API_KEY is required")
		return 1
	}

	tools := map[string]Tool{}
	promURL := os.Getenv("PROM_URL")

	if promURL == "" {
		slog.Warn("no PROM_URL - clank will run without evidence tools; every proposal will gate to no_action")
	} else if eqPath := os.Getenv("EVIDENCE_QUERIES"); eqPath != "" {
		queries, err := LoadEvidenceQueries(eqPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load evidence queries: %v\n", err)
			return 1
		}
		tools["metrics"] = &MetricsTool{
			BaseURL: promURL,
			Queries: queries,
		}
	}

	config, err := rest.InClusterConfig()
	if err == nil {
		kubeClient, err := kubernetes.NewForConfig(config)
		if err == nil {
			tools["kube"] = &KubeTool{Client: kubeClient}
		} else {
			slog.Warn("could not build kube client from InClusterConfig", "err", err)
		}
	} else {
		slog.Info("not running in-cluster, skipping kube tool registration")
	}

	model := NewAnthropicModel(apiKey)
	intake := NewIntake(noopTopology{}, noopChange{})
	if catPath, sqPath := os.Getenv("WHIR_CATALOG"), os.Getenv("WHIR_STATE_QUERIES"); catPath != "" && sqPath != "" {
		promURL := os.Getenv("PROM_URL")
		if promURL == "" {
			_, _ = fmt.Fprintln(stderr, "PROM_URL required when WHIR_CATALOG and WHIR_STATE_QUERIES are set")
			return 1
		}
		cat, err := whir.LoadCatalogFile(catPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load whir catalog: %v\n", err)
			return 1
		}
		queries, err := whir.LoadStateQueries(sqPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load whir state queries: %v\n", err)
			return 1
		}
		intake = NewIntake(WhirTopology{
			Catalog:  cat,
			Resolver: &whir.Resolver{BaseURL: promURL, Client: http.DefaultClient, Queries: queries},
		}, noopChange{})
	}

	var store Store = NewMemStore()
	if transcripts != "" {
		if err := os.MkdirAll(transcripts, 0o750); err != nil { //nolint:gosec
			_, _ = fmt.Fprintf(stderr, "mkdir transcripts: %v", err)
			return 1
		}
		store = NewDirStore(transcripts)
	}

	natsURL := os.Getenv("NATS_URL")
	if natsURL != "" {
		return runBroker(ctx, natsURL, model, intake, store, tools, stderr)
	}

	// offline path: the dir-glob Transport is now the keyless fake the
	// seam tests exercise — broker mode above is how this actually runs.
	// CLANK_INBOX/OUTBOX/OUTCOMES are this path's env, not the process's —
	// checked here, not above, so broker mode never has to satisfy them
	// (mirrors rattle.go/hiss.go/thump.go's NATS_URL-first branch).
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

	l := newLoop(inbox, outbox, outcomes, model, tools, intake, defaultCatalog(), store)
	tr := &Transport{Inbox: inbox, Engine: l.Engine}
	runLoop(ctx, tr, l.ReturnEdge)
	return 0
}

func runBroker(ctx context.Context, natsURL string, model Model, intake *Intake, store Store, tools map[string]Tool, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	walDir := os.Getenv("WAL_DIR")
	if walDir == "" {
		_, _ = fmt.Fprintln(stderr, "WAL_DIR is required")
		return 1
	}
	w := &publish.WAL{Dir: walDir, Beat: "clank", Subject: "thump.proposals"}
	defer func() { _ = w.Close(ctx) }()

	proposalPub := &publish.WALPublisher[ProposalSet]{
		WAL:  w,
		Next: publish.NewJetPublisher[ProposalSet](js),
	}

	ledger := NewMemProposalLog()
	cases := NewCaseBase()
	learn := Click{Ledger: ledger, Cases: cases}

	eng := newBrokerEngine(model, intake, store, tools, proposalPub, ledger, cases)

	g, gctx := errgroup.WithContext(ctx)

	detSub := broker.NewJetSubscriber[signal.Detection](js)
	g.Go(func() error {
		return detSub.Run(gctx, "thump.detections", func(ctx context.Context, det signal.Detection) error {
			set, err := eng.Propose(ctx, det)
			if err != nil {
				return err
			}
			gatePassed := set.Gate != nil && set.Gate.Passed
			slog.Info("reasoned", "fingerprint", det.Fingerprint, "phase", set.Status.Phase, "recommended", set.Recommended, "gatePassed", gatePassed, "reason", set.Status.Reason, "evidence", len(set.Evidence))
			return nil
		})
	})

	outSub := broker.NewJetSubscriber[outcome.Outcome](js)
	g.Go(func() error {
		return outSub.Run(gctx, "thump.outcomes", func(ctx context.Context, o outcome.Outcome) error {
			return learnHandler(ctx, learn, o) // maps Absorb's errors to Ack/transient — see below
		})
	})

	if err := g.Wait(); err != nil && ctx.Err() == nil {
		slog.Error("broker run failed", "err", err)
		return 1
	}
	return 0
}

const (
	backoffBase = 5 * time.Second
	backoffCap  = 5 * time.Minute
)

func learnHandler(ctx context.Context, c Click, o outcome.Outcome) error {
	switch err := c.Absorb(ctx, o); {
	case err == nil:
		return nil // matched + learned → Ack (was: processed/)
	case errors.Is(err, ErrNoOpenSet):
		slog.Warn("outcome arrived with no open set", "fingerprint", o.SignalRef)
		return nil // terminal in dir mode (unmatched/) → Ack, don't retry-forever
	default: // unauditable / incoherent — deterministic, a real seam bug
		slog.Error("outcome failed absorb", "fingerprint", o.SignalRef, "err", err)
		return nil // was: quarantine/ — terminal, so Ack (NOT an error; erroring would DLQ-after-6)
	}
}

func nextDelay(cur time.Duration, tickOK bool) time.Duration {
	if tickOK {
		return backoffBase
	}
	return min(cur*2, backoffCap)
}

// runLoop is Main's ticker-driven body, pulled into its own function so it
// takes a context directly — a test can cancel that context and observe a
// prompt return, with no OS signals involved (Main's ctx comes from
// NotifyContext; this one doesn't have to).
func runLoop(ctx context.Context, tr *Transport, re *ReturnEdge) {
	delay := backoffBase
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return
		case <-timer.C:
			tickErr := tr.Tick(ctx)
			if tickErr != nil {
				slog.Error("tick failed", "err", tickErr)
			}
			if err := re.Tick(ctx); err != nil {
				slog.Error("learn tick failed", "err", err)
			}
			delay = nextDelay(delay, tickErr == nil)
			if tickErr != nil {
				delay += rand.N(delay / 4) //nolint:gosec
			}
			timer.Reset(delay)
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

type loop struct {
	Engine       *Engine
	ReturnEdge   *ReturnEdge
	Cases        *CaseBase
	OutcomeInbox string
}

func newLoop(_, outbox, outcomes string, model Model, tools map[string]Tool, intake *Intake, cat *StaticCatalog, store Store) *loop {
	ledger := NewMemProposalLog() // ONE ledger
	cases := NewCaseBase()        // ONE case base
	eng := &Engine{
		Intake:       intake,
		Model:        model,
		Tools:        tools,
		Catalog:      cat,
		Ranker:       NewRanker(),
		Store:        store,
		Scorer:       &CausalScorerImpl{Prior: cases}, // scorer reads THIS case base
		DedupeWindow: time.Hour,
		Ledger:       ledger, // engine records into THIS ledger
		Pub:          &DirPublisher{Dir: outbox},
		Gate:         ReadinessGate{},
		MaxSteps:     8,
	}
	re := &ReturnEdge{
		Inbox: outcomes, // thump's outbox — NOT outbox, which is hiss's inbox
		Click: Click{Ledger: ledger, Cases: cases},
	}
	return &loop{Engine: eng, ReturnEdge: re, Cases: cases, OutcomeInbox: outcomes}
}

// TODO: move this.
func newBrokerEngine(model Model, intake *Intake, store Store, tools map[string]Tool, pub *publish.WALPublisher[ProposalSet], ledger *MemProposalLog, cases *CaseBase) *Engine {
	return &Engine{
		Intake:       intake,
		Model:        model,
		Tools:        tools, // <-- Here's our plug!
		Catalog:      defaultCatalog(),
		Ranker:       NewRanker(),
		Store:        store,
		Scorer:       &CausalScorerImpl{Prior: cases},
		DedupeWindow: time.Hour,
		Ledger:       ledger,
		Pub:          pub,
		Gate:         ReadinessGate{},
		MaxSteps:     8,
	}
}
