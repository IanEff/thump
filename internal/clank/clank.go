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
	"github.com/ianeff/thump/internal/config"
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

	cfg, err := config.LoadClank(lc.NATSURL != "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if cfg.Transcripts == "" {
		slog.Info("CLANK_TRANSCRIPTS not set — turns held in memory, not persisted")
	}

	cat, err := contract.LoadCatalogFile(cfg.ActionCatalog, contract.Preconditions)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load action catalog: %v\n", err)
		return 1
	}

	tools := map[string]Tool{}
	if cfg.PromURL == "" {
		slog.Warn("no PROM_URL - clank will run without evidence tools; every proposal will gate to no_action")
	} else if cfg.EvidenceQueries != "" {
		queries, err := LoadEvidenceQueries(cfg.EvidenceQueries)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load evidence queries: %v\n", err)
			return 1
		}
		tools["metrics"] = &MetricsTool{
			BaseURL: cfg.PromURL,
			Queries: queries,
		}
	}

	if cfg.LokiURL == "" {
		slog.Warn("no LOKI_URL - clank will run without evidence tools; every proposal gate will take no_action")
	} else {
		tools["loki"] = &LokiTool{BaseURL: cfg.LokiURL}
	}

	restConfig, err := rest.InClusterConfig()
	if err == nil {
		kubeClient, err := kubernetes.NewForConfig(restConfig)
		if err == nil {
			tools["kube"] = &KubeTool{Client: kubeClient}
		} else {
			slog.Warn("could not build kube client from InClusterConfig", "err", err)
		}
	} else {
		slog.Info("not running in-cluster, skipping kube tool registration")
	}

	model := NewAnthropicModel(cfg.AnthropicAPIKey)
	intake := NewIntake(noopTopology{}, noopChange{})
	if cfg.WhirCatalog != "" && cfg.WhirStateQueries != "" {
		if cfg.PromURL == "" {
			_, _ = fmt.Fprintln(stderr, "PROM_URL required when WHIR_CATALOG and WHIR_STATE_QUERIES are set")
			return 1
		}
		cat, err := whir.LoadCatalogFile(cfg.WhirCatalog)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load whir catalog: %v\n", err)
			return 1
		}
		queries, err := whir.LoadStateQueries(cfg.WhirStateQueries)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load whir state queries: %v\n", err)
			return 1
		}
		intake = NewIntake(WhirTopology{
			Catalog:  cat,
			Resolver: &whir.Resolver{BaseURL: cfg.PromURL, Client: http.DefaultClient, Queries: queries},
		}, noopChange{})
	}

	var store Store = NewMemStore()
	if cfg.Transcripts != "" {
		if err := os.MkdirAll(cfg.Transcripts, 0o750); err != nil { //nolint:gosec
			_, _ = fmt.Fprintf(stderr, "mkdir transcripts: %v", err)
			return 1
		}
		store = NewDirStore(cfg.Transcripts)
	}

	tracer, shutdownTracer, err := beat.Tracer(ctx, "clank")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v\n", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	reg, shutdownMetrics := beat.Metrics("clank")
	defer func() { _ = shutdownMetrics(ctx) }()
	recorder := NewRecorder(reg)
	stages := beat.NewStageRecorder(reg)

	if lc.NATSURL != "" {
		return runBroker(ctx, lc.NATSURL, model, intake, store, tools, cat, tracer, recorder, stages, stderr)
	}

	// offline path: the dir-glob Transport is now the keyless fake the
	// seam tests exercise — broker mode above is how this actually runs.
	// cfg.Inbox/Outbox/Outcomes are this path's env, not the process's —
	// config.LoadClank only requires them when broker is false (mirrors
	// rattle.go/hiss.go/thump.go's NATS_URL-first branch).
	l := newLoop(cfg.Inbox, cfg.Outbox, cfg.Outcomes, model, tools, intake, cat, store, tracer, stages)
	tr := &Transport{Inbox: cfg.Inbox, Engine: l.Engine}
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
