package clank

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/whir"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Main is clank's process entry. It wires the Model (Anthropic, keyed by
// ANTHROPIC_API_KEY — Main refuses to start without it), the read-only tools
// (metrics, loki, kube — each registered only if its backend is configured,
// so a partial deployment loses tools, not the process), the intake sources,
// and the Store, then runs either the NATS broker path or the directory-poll
// path depending on whether the beat kit resolved a NATS URL. It returns a
// process exit code rather than calling os.Exit, so tests can drive it.
func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	lc, code, exit := beat.Start("clank", args, stdout, stderr, beat.Version{Version: version, Commit: commit, Date: date})
	if exit {
		return code
	}
	defer lc.Stop()
	ctx := lc.Ctx

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

	lokiURL := os.Getenv("LOKI_URL")
	if lokiURL == "" {
		slog.Warn(("no LOKI_URL - clank will run without evidence tools; every proposal gate will take no_action"))
	} else {
		tools["loki"] = &LokiTool{BaseURL: lokiURL}
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

	tracer, shutdownTracer, err := beat.Tracer(ctx, "clank")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v\n", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	if lc.NATSURL != "" {
		return runBroker(ctx, lc.NATSURL, model, intake, store, tools, tracer, stderr)
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

	l := newLoop(inbox, outbox, outcomes, model, tools, intake, contract.Default(), store, tracer)
	tr := &Transport{Inbox: inbox, Engine: l.Engine}
	re := l.ReturnEdge

	// One dir-poll cycle drives both the forward transport (a detection is
	// reasoned into a proposal) and the return edge (an outcome is absorbed).
	// Only the forward tick governs the backoff — a failing inbox source is
	// what should slow the loop down; the return edge runs every cycle
	// regardless.
	tick := func(ctx context.Context) error {
		tickErr := tr.Tick(ctx)
		if err := re.Tick(ctx); err != nil {
			slog.Error("learn tick failed", "err", err)
		}
		return tickErr
	}
	beat.PollLoop(ctx, beat.PollConfig{Backoff: &beat.BackoffConfig{
		Base:          5 * time.Second,
		Cap:           5 * time.Minute,
		JitterDivisor: 4,
	}}, tick)
	return 0
}
