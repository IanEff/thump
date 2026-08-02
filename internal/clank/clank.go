package clank

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/otelx"
	"github.com/ianeff/thump/internal/poll"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/whir"
	"k8s.io/client-go/dynamic"
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
	if lc.NATSURL == "" && cfg.Transcripts == "" {
		slog.Info("CLANK_TRANSCRIPTS not set — turns held in memory, not persisted")
	}

	cat, err := contract.LoadCatalogFile(cfg.ActionCatalog, contract.Preconditions)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load action catalog: %v\n", err)
		return 1
	}

	classes, err := contract.LoadFailureClassesFile(cfg.FailureClasses)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load failure classes: %v\n", err)
		return 1
	}

	weights, err := LoadWeightsFile(cfg.Weights)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load weights: %v\n", err)
		return 1
	}

	limits, err := LoadLimitsFile(cfg.Limits)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load limits: %v\n", err)
		return 1
	}

	// backendTLS is nil in the offline path (cfg.TLSCertFile unset) and dials
	// PROM_URL/LOKI_URL in the clear, same as today — L4/L5's declared
	// exception. In the broker path it's the beat's own leaf, ready the day
	// either backend starts serving TLS from the cluster's private CA.
	var backendTLS *tls.Config
	if cfg.TLSCertFile != "" {
		backendTLS, err = tlsx.Client(tlsx.Config{
			CertFile: cfg.TLSCertFile,
			KeyFile:  cfg.TLSKeyFile,
			CAFile:   cfg.TLSCAFile,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "backend tls setup: %v\n", err)
			return 1
		}
	}

	var ev EvidenceConfig
	if cfg.EvidenceQueries != "" {
		ev, err = LoadEvidenceConfig(cfg.EvidenceQueries)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load evidence queries: %v\n", err)
			return 1
		}
	}

	kubeClient, argoClient := inClusterClients()
	tools := buildTools(cfg, backendTLS, ev, kubeClient)

	model := NewAnthropicModel(cfg.AnthropicAPIKey)
	intake, err := buildIntake(cfg, backendTLS, argoClient, ev.Index, limits.ChangeLookback)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build intake: %v\n", err)
		return 1
	}

	// Broker mode already Require()s all four S3_* vars for the WAL shipper
	// (config.LoadClank), so transcripts always ride the same bucket there —
	// durable-by-default, no separate opt-in. The offline dir-poll path has
	// no S3 creds to reach for, so CLANK_TRANSCRIPTS keeps its original,
	// narrower meaning: a local directory, or memory if unset.
	var store Store = NewMemStore()
	switch {
	case lc.NATSURL != "":
		client, err := objectstore.NewS3Client(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "transcripts s3 client: %v\n", err)
			return 1
		}
		store = NewS3Store(client, cfg.S3Bucket, sealbox.Key(cfg.SealKey))
	case cfg.Transcripts != "":
		if err := os.MkdirAll(cfg.Transcripts, 0o750); err != nil { //nolint:gosec // G301: operator-configured directory, not user input
			_, _ = fmt.Fprintf(stderr, "mkdir transcripts: %v", err)
			return 1
		}
		store = NewDirStore(cfg.Transcripts)
	}

	tracer, shutdownTracer, err := otelx.Tracer(ctx, "clank", cfg.OTLPEndpoint, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v\n", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	var metricsTLS *tls.Config
	if cfg.TLSCertFile != "" {
		metricsTLS, err = tlsx.Server(tlsx.Config{
			CertFile: cfg.TLSCertFile,
			KeyFile:  cfg.TLSKeyFile,
			CAFile:   cfg.TLSCAFile,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "metrics tls setup: %v\n", err)
			return 1
		}
	}
	reg, health, shutdownMetrics := beat.Metrics("clank", metricsTLS)
	defer func() { _ = shutdownMetrics(ctx) }()
	recorder := NewRecorder(reg)
	stages := beat.NewStageRecorder(reg)

	if lc.NATSURL != "" {
		return runBroker(ctx, cfg.NATSURL, cfg, model, intake, store, tools, cat, classes, weights, limits, tracer, recorder, stages, health, stderr)
	}

	health.SetReady(true)

	// offline path: the dir-glob Transport is now the keyless fake the
	// seam tests exercise — broker mode above is how this actually runs.
	// cfg.Inbox/Outbox/Outcomes are this path's env, not the process's —
	// config.LoadClank only requires them when broker is false (mirrors
	// rattle.go/hiss.go/thump.go's NATS_URL-first branch).
	l := newLoop(cfg.Inbox, cfg.Outbox, cfg.Outcomes, cfg.Declines, model, tools, intake, cat, classes, store, cfg.DedupeWindow, tracer, stages, recorder, weights, limits)
	tr := &Transport{Inbox: cfg.Inbox, Engine: l.Engine, MaxProposeAttempts: limits.MaxProposeAttempts}
	re := l.ReturnEdge
	de := l.DeclineEdge

	// One dir-poll cycle drives the forward transport (a detection is
	// reasoned into a proposal) and both return edges — the outcome edge
	// (an outcome is absorbed) and the decline edge (a non-approval closes
	// the ledger's dedup window). Only the forward tick governs the
	// backoff — a failing inbox source is what should slow the loop down;
	// the return edges run every cycle regardless.
	tick := func(ctx context.Context) error {
		tickErr := tr.Tick(ctx)
		if err := re.Tick(ctx); err != nil {
			slog.Error("learn tick failed", "err", err)
		}
		if err := de.Tick(ctx); err != nil {
			slog.Error("decline tick failed", "err", err)
		}
		return tickErr
	}
	poll.Loop(ctx, poll.Config{
		Backoff: &poll.BackoffConfig{
			Base:          5 * time.Second,
			Cap:           5 * time.Minute,
			JitterDivisor: 4,
		},
		// Longer than modelRequestTimeout — a shorter tick timeout would fire
		// first on every call, and the model's own 120s budget would never be
		// reached.
		Timeout: modelRequestTimeout + 30*time.Second,
	}, tick)
	return 0
}

// inClusterClients returns the typed client the kube evidence tool needs and
// the dynamic client the ArgoCD change source needs, or nil for either when
// clank isn't running as a pod. Every failure here degrades one capability
// rather than stopping the beat, so each one says so at WARN and names what
// is now missing.
func inClusterClients() (kubernetes.Interface, dynamic.Interface) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		slog.Info("not running in-cluster — no kube evidence tool and no change source", "beat", "clank")
		return nil, nil
	}
	return clientsFor(restConfig)
}

// clientsFor builds both clients from an already-resolved rest.Config,
// returning a nil interface for either one that fails. Each is assigned
// through its interface type and never through the concrete pointer: a nil
// *Clientset handed back as a kubernetes.Interface is a non-nil interface,
// and a caller's nil check would then wire a tool around a client that panics
// on first use.
func clientsFor(restConfig *rest.Config) (kubernetes.Interface, dynamic.Interface) {
	var kube kubernetes.Interface
	if kubeClient, err := kubernetes.NewForConfig(restConfig); err != nil {
		slog.Warn("could not build a kube client — clank reasoning without cluster-object evidence",
			"beat", "clank", "err", err)
	} else {
		kube = kubeClient
	}

	var argo dynamic.Interface
	if argoClient, err := dynamic.NewForConfig(restConfig); err != nil {
		slog.Warn("could not build a dynamic client — causal scoring is inert",
			"beat", "clank", "err", err)
	} else {
		argo = argoClient
	}
	return kube, argo
}

// buildTools assembles clank's read-only evidence tools from cfg — each one
// registered only if its backend is configured, so a partial deployment loses
// a tool rather than the process. The subject rules ride along with every
// tool that can carry one: a tool holding an empty index returns Live refs
// that can corroborate but never ground, which is a degradation worth saying
// out loud rather than discovering from a gate that never passes.
func buildTools(cfg config.Clank, backendTLS *tls.Config, ev EvidenceConfig, kube kubernetes.Interface) map[string]Tool {
	tools := map[string]Tool{}

	switch {
	case cfg.PromURL == "":
		slog.Warn("no PROM_URL - clank will run without evidence tools; every proposal will gate to no_action")
	case cfg.EvidenceQueries == "":
		slog.Warn("no EVIDENCE_QUERIES — clank has a Prometheus but no named queries to ask it",
			"beat", "clank", "fix", "set EVIDENCE_QUERIES")
	default:
		tools["metrics"] = &MetricsTool{
			BaseURL: cfg.PromURL,
			Queries: ev.Queries,
			Client:  httpx.Client(httpx.DefaultBackendTimeout, backendTLS),
		}
	}

	if cfg.LokiURL == "" {
		slog.Warn("no LOKI_URL - clank will run without evidence tools; every proposal gate will take no_action")
	} else {
		tools["loki"] = &LokiTool{
			BaseURL:  cfg.LokiURL,
			Client:   httpx.Client(httpx.DefaultBackendTimeout, backendTLS),
			Subjects: ev.Index,
		}
	}

	if kube != nil {
		tools["kube"] = &KubeTool{Client: kube, Subjects: ev.Index}
	}

	if len(ev.Index) == 0 && (tools["loki"] != nil || tools["kube"] != nil) {
		slog.Warn("no subject rules configured — loki and kube evidence can corroborate but never ground",
			"beat", "clank", "fix", "add a subjects: block to EVIDENCE_QUERIES")
	}

	return tools
}

// buildIntake assembles clank's Intake from cfg — WhirTopology when
// WHIR_CATALOG and WHIR_STATE_QUERIES are both set, noopTopology otherwise,
// and ArgoChangeSource only when clank both wants a change source and has the
// in-cluster identity to reach one.
// buildIntake assumes cfg has already passed config.LoadClank's validation —
// PROM_URL is cross-required there whenever both whir vars are set, so this
// never needs to check that combination itself.
func buildIntake(cfg config.Clank, backendTLS *tls.Config, argo dynamic.Interface, subjects SubjectIndex, changeLookback time.Duration) (*Intake, error) {
	var topo TopologySource = noopTopology{}

	if cfg.WhirCatalog == "" || cfg.WhirStateQueries == "" {
		slog.Warn("no topology source configured — clank reasoning without a blast-radius map",
			"beat", "clank", "fix", "set WHIR_CATALOG and WHIR_STATE_QUERIES")
	} else {
		cat, err := whir.LoadCatalogFile(cfg.WhirCatalog)
		if err != nil {
			return nil, fmt.Errorf("load whir catalog: %w", err)
		}
		queries, err := whir.LoadStateQueries(cfg.WhirStateQueries)
		if err != nil {
			return nil, fmt.Errorf("load whir state queries: %w", err)
		}
		topo = WhirTopology{
			Catalog:  cat,
			Resolver: &whir.Resolver{BaseURL: cfg.PromURL, Client: httpx.Client(httpx.DefaultBackendTimeout, backendTLS), Queries: queries},
		}
	}

	var change ChangeSource = noopChange{}
	switch {
	case !cfg.ArgoEnabled:
		slog.Warn("no change source configured — causal scoring is inert", "beat", "clank", "fix", "set ARGOCD_ENABLED=true")
	case argo == nil:
		slog.Warn("ARGOCD_ENABLED is set but clank has no in-cluster identity — causal scoring is inert", "beat", "clank")
	case len(subjects) == 0:
		// A change source with nothing to resolve against reports events whose
		// targets name Kubernetes objects the topology graph has never heard
		// of, so every score lands out of topology and the causal term drops
		// out — inert, but inert while looking configured.
		slog.Warn("no subject rules authored — every change event will resolve outside the topology and causal scoring is inert",
			"beat", "clank", "fix", "author subjects: in the file EVIDENCE_QUERIES names")
	default:
		change = ArgoChangeSource{Client: argo, Subjects: subjects, ChangeLookback: changeLookback}
	}

	return NewIntake(topo, change), nil
}
