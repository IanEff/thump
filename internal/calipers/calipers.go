package calipers

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/corpus"
	"github.com/ianeff/thump/internal/harvest"
	"github.com/ianeff/thump/internal/incident"
	"github.com/ianeff/thump/internal/mock"
	"github.com/ianeff/thump/internal/pipeline"
	"github.com/ianeff/thump/internal/probe"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/rca"
	"github.com/ianeff/thump/internal/replay"
	"github.com/ianeff/thump/internal/scorecard"
	"github.com/ianeff/thump/internal/step"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/transcript"
	"github.com/ianeff/thump/internal/tune"
	"github.com/ianeff/thump/internal/unseal"
	"github.com/ianeff/thump/internal/validate"
)

// shiftPositional pulls the leading fingerprint argument off args so a
// flag.FlagSet can parse the rest — stdlib flag.Parse stops at the first
// non-flag argument, so "approve <fp> --approver alice" has to have <fp>
// split off before Parse runs or the flags after it are swallowed as
// positional args instead of being recognized.
func shiftPositional(args []string) (positional string, rest []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, false
	}
	return args[0], args[1:], true
}

// operatorPublisher is approve's and force's shared choice between the
// broker and the local outbox: a set natsURL means the operator surface is
// reachable — hiss and thump read JetStream in broker mode, not a
// directory — so the same fingerprint written to --outbox there would be
// read by nothing. Empty natsURL preserves the offline path unchanged.
// The returned closer is always safe to defer, including the dir path.
func operatorPublisher[T any](natsURL, certFile, keyFile, caFile, serverName, outbox string, name func(T) string) (publish.Publisher[T], func(), error) {
	if natsURL == "" {
		return &publish.DirPublisher[T]{Dir: outbox, Name: name}, func() {}, nil
	}
	tc := tlsx.Config{CertFile: certFile, KeyFile: keyFile, CAFile: caFile, ServerName: serverName}
	js, closer, err := broker.Connect(context.Background(), natsURL, tc, broker.Hooks{})
	if err != nil {
		return nil, func() {}, fmt.Errorf("connect %s: %w", natsURL, err)
	}
	return publish.NewJetPublisher[T](js), closer, nil
}

// operatorProjection is incidents's and force's shared choice of read
// backend, mirroring operatorPublisher's choice of write backend — the two
// resolve from the same natsURL so a break-glass can never read a directory
// while publishing to a broker, which is how force came to report every
// held fingerprint as absent.
func operatorProjection(ctx context.Context, natsURL, certFile, keyFile, caFile, serverName, inbox string) (*incident.Projection, func(), error) {
	if natsURL == "" {
		tr := &Transport{Inbox: inbox}
		proj, err := tr.Snapshot(ctx)
		if err != nil {
			return nil, func() {}, err
		}
		return proj, func() {}, nil
	}
	tc := tlsx.Config{CertFile: certFile, KeyFile: keyFile, CAFile: caFile, ServerName: serverName}
	js, closer, err := broker.Connect(ctx, natsURL, tc, broker.Hooks{})
	if err != nil {
		return nil, func() {}, fmt.Errorf("connect %s: %w", natsURL, err)
	}
	proj, err := incident.SnapshotBroker(ctx, js)
	if err != nil {
		closer()
		return nil, func() {}, fmt.Errorf("snapshot broker: %w", err)
	}
	return proj, closer, nil
}

// topUsage is calipers's top-level help line — pinned against the switch
// below by TestMain_RoutesEveryDocumentedVerbAndRefusesTheRest so the two
// cannot drift the way trim's own usage string once did (it named only
// "incidents" while the switch routed four verbs).
const topUsage = "usage: calipers <incidents|approve|force|unseal|corpus|rca|tune|replay|harvest|probe|transcript|scorecard|validate|step|pipeline|mock> [flags]"

// Main is calipers's entry point: routing to subcommand, then
// either the machine (--json) or human (Lip Gloss) path over
// the same Projection.
// It returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return MainContext(ctx, args, stdout, stderr)
}

// MainContext executes calipers subcommands within an explicit context —
// cancels running mocks or long-running operations when the context expires.
func MainContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, topUsage)
		return 2
	}

	switch args[0] {
	case "incidents":
		return runIncidents(args[1:], stdout, stderr)
	case "approve":
		return runApprove(args[1:], stdout, stderr)
	case "force":
		return runForce(args[1:], stdout, stderr)
	case "unseal":
		return unseal.Main(args[1:], stdout, stderr)
	case "corpus":
		return corpus.Main(args[1:], stdout, stderr)
	case "rca":
		return rca.Main(args[1:], stdout, stderr)
	case "tune":
		return tune.Main(args[1:], stdout, stderr)
	case "replay":
		return replay.Main(args[1:], stdout, stderr)
	case "harvest":
		return harvest.Main(args[1:], stdout, stderr)
	case "probe":
		return probe.Main(args[1:], stdout, stderr)
	case "transcript":
		return transcript.Main(args[1:], stdout, stderr)
	case "scorecard":
		return scorecard.Main(args[1:], stdout, stderr)
	case "validate":
		return validate.Main(args[1:], stdout, stderr)
	case "step":
		return runStep(ctx, args[1:], stdout, stderr)
	case "pipeline":
		return runPipeline(ctx, args[1:], stdout, stderr)
	case "mock":
		return runMock(ctx, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, topUsage)
		return 2
	}
}

func runStep(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: calipers step <rattle|clank|hiss|thump> [flags]"
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	switch args[0] {
	case "rattle":
		return runStepRattle(ctx, args[1:], stdout, stderr)
	case "clank":
		return runStepClank(ctx, args[1:], stdout, stderr)
	case "hiss":
		return runStepHiss(ctx, args[1:], stdout, stderr)
	case "thump":
		return runStepThump(ctx, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
}

func runStepRattle(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("step rattle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	watch := fs.String("watch", "config/dev/rattle/watch.yaml", "path to watch.yaml")
	queryConfig := fs.String("query-config", "config/dev/rattle/query.yaml", "path to query.yaml")
	queries := fs.String("queries", "", "alias for --query-config")
	defaultProm := os.Getenv("PROM_URL")
	if defaultProm == "" {
		defaultProm = "http://localhost:9090"
	}
	promURL := fs.String("prom-url", defaultProm, "Prometheus base URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	qPath := *queryConfig
	if *queries != "" {
		qPath = *queries
	}
	if fs.NArg() > 0 && *watch == "config/dev/rattle/watch.yaml" {
		*watch = fs.Arg(0)
	}
	if fs.NArg() > 1 && qPath == "config/dev/rattle/query.yaml" {
		qPath = fs.Arg(1)
	}
	if fs.NArg() > 2 && *promURL == defaultProm {
		*promURL = fs.Arg(2)
	}

	det, err := step.RunRattle(ctx, *watch, qPath, *promURL)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(det); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	return 0
}

func runStepClank(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: calipers step clank --detection <path> [--profile dir] [--model name] [--api-key key]"
	fs := flag.NewFlagSet("step clank", flag.ContinueOnError)
	fs.SetOutput(stderr)
	detection := fs.String("detection", "", "path to detection JSON/YAML file")
	profile := fs.String("profile", "config/dev", "path to profile directory")
	model := fs.String("model", "haiku", "Anthropic model name")
	apiKey := fs.String("api-key", os.Getenv("ANTHROPIC_API_KEY"), "Anthropic API key (defaults to ANTHROPIC_API_KEY env var)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *detection == "" && fs.NArg() > 0 {
		*detection = fs.Arg(0)
	}
	if *detection == "" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	ps, err := step.RunClank(ctx, *detection, *profile, *model, *apiKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ps); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	return 0
}

func runStepHiss(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: calipers step hiss --proposal <path> [--policy <path>]"
	fs := flag.NewFlagSet("step hiss", flag.ContinueOnError)
	fs.SetOutput(stderr)
	propFile := fs.String("proposal", "", "path to proposal JSON/YAML file")
	polFile := fs.String("policy", "config/dev/hiss/policy.yaml", "path to policy.yaml file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *propFile == "" && fs.NArg() > 0 {
		*propFile = fs.Arg(0)
	}
	if *polFile == "config/dev/hiss/policy.yaml" && fs.NArg() > 1 {
		*polFile = fs.Arg(1)
	}
	if *propFile == "" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	gov, err := step.RunHiss(ctx, *propFile, *polFile)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(gov); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	return 0
}

func runStepThump(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: calipers step thump --decision <path> [--catalog <path>] [--dry-run=true|false]"
	fs := flag.NewFlagSet("step thump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	decFile := fs.String("decision", "", "path to decision JSON/YAML file")
	catFile := fs.String("catalog", "config/dev/actions/catalog.yaml", "path to catalog.yaml file")
	dryRun := fs.Bool("dry-run", true, "dry-run execution mode")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *decFile == "" && fs.NArg() > 0 {
		*decFile = fs.Arg(0)
	}
	if *catFile == "config/dev/actions/catalog.yaml" && fs.NArg() > 1 {
		*catFile = fs.Arg(1)
	}
	if *decFile == "" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	out, err := step.RunThump(ctx, *decFile, *catFile, *dryRun)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	return 0
}

func runPipeline(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	usage := "usage: calipers pipeline --detection <path> [--profile dir] [--model name] [--api-key key]"
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	detection := fs.String("detection", "", "path to detection JSON/YAML file")
	profile := fs.String("profile", "config/dev", "path to profile directory")
	model := fs.String("model", "haiku", "Anthropic model name")
	apiKey := fs.String("api-key", os.Getenv("ANTHROPIC_API_KEY"), "Anthropic API key (defaults to ANTHROPIC_API_KEY env var)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *detection == "" && fs.NArg() > 0 {
		*detection = fs.Arg(0)
	}
	if *detection == "" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	res, err := pipeline.Run(ctx, *detection, *profile, *model, *apiKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	return 0
}

func runMock(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mock", flag.ContinueOnError)
	fs.SetOutput(stderr)
	promPort := fs.Int("prom-port", 9090, "HTTP port for Prometheus/Loki telemetry stub server")
	enableNATS := fs.Bool("nats", false, "start embedded NATS JetStream broker")
	natsPort := fs.Int("nats-port", 4222, "TCP port for embedded NATS server")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ts, err := mock.NewTelemetryServer(mock.WithPort(*promPort))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	defer func() {
		if err := ts.Close(); err != nil {
			slog.Warn("close mock telemetry server", "err", err)
		}
	}()

	_, _ = fmt.Fprintf(stdout, "mock telemetry server listening on %s\n", ts.URL())

	if *enableNATS {
		brk, err := mock.NewEmbeddedBroker(mock.WithBrokerPort(*natsPort))
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "calipers:", err)
			return 1
		}
		defer func() {
			if err := brk.Close(); err != nil {
				slog.Warn("close mock broker", "err", err)
			}
		}()
		_, _ = fmt.Fprintf(stdout, "mock nats broker listening on %s\n", brk.ClientURL())
	}

	<-ctx.Done()
	return 0
}

func runIncidents(args []string, stdout, stderr io.Writer) int {
	var fp string
	rest := args
	if f, r, ok := shiftPositional(args); ok {
		fp, rest = f, r
	}

	fs := flag.NewFlagSet("incidents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print incidents as JSON")
	render := fs.String("render", "", "render full incident audit record for fingerprint")
	inbox := fs.String("inbox", ".", "directory calipers polls for boundary objects")
	natsURL := fs.String("nats-url", "", "read the live queue over NATS instead of --inbox")
	certFile := fs.String("tls-cert", "", "client cert, required with --nats-url")
	keyFile := fs.String("tls-key", "", "client key, required with --nats-url")
	caFile := fs.String("tls-ca", "", "CA bundle, required with --nats-url")
	serverName := fs.String("server-name", "", "TLS SAN to verify the peer against, if it differs from the dialed host (e.g. a port-forwarded nats-url)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	proj, closer, err := operatorProjection(context.Background(), *natsURL, *certFile, *keyFile, *caFile, *serverName, *inbox)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	defer closer()

	targetRenderFP := *render
	if targetRenderFP != "" {
		rec, ok := proj.GetRecord(targetRenderFP)
		if !ok {
			_, _ = fmt.Fprintf(stderr, "calipers: no incident at fingerprint %s\n", targetRenderFP)
			return 1
		}
		if err := incident.Render(stdout, rec); err != nil {
			_, _ = fmt.Fprintln(stderr, "calipers:", err)
			return 1
		}
		return 0
	}

	if fp != "" {
		inc, ok := proj.Get(fp)
		if !ok {
			_, _ = fmt.Fprintf(stderr, "calipers: no incident at fingerprint %s\n", fp)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, renderIncidentDetail(inc, time.Now()))
		return 0
	}

	incidents := proj.Snapshot()
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(incidents); err != nil {
			_, _ = fmt.Fprintln(stderr, "calipers:", err)
			return 1
		}
		return 0
	}

	_, _ = fmt.Fprintln(stdout, renderIncidents(incidents, time.Now()))
	return 0
}

func runApprove(args []string, stdout, stderr io.Writer) int {
	usage := "usage: calipers approve <fingerprint> [--approver name] [--outbox dir] [--nats-url url] [--tls-cert path --tls-key path --tls-ca path --server-name name]"
	fp, rest, ok := shiftPositional(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	approver := fs.String("approver", os.Getenv("USER"), "who is approving")
	outbox := fs.String("outbox", ".", "directory calipers writes the Approval to (thump.approvals in production)")
	natsURL := fs.String("nats-url", "", "publish to thump.approvals over NATS instead of --outbox")
	certFile := fs.String("tls-cert", "", "client cert, required with --nats-url")
	keyFile := fs.String("tls-key", "", "client key, required with --nats-url")
	caFile := fs.String("tls-ca", "", "CA bundle, required with --nats-url")
	serverName := fs.String("server-name", "", "TLS SAN to verify the peer against, if it differs from the dialed host (e.g. a port-forwarded nats-url)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	a := approval.Approval{SignalRef: fp, Approver: *approver, ApprovedAt: time.Now()}
	if err := a.Auditable(); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	pub, closer, err := operatorPublisher(*natsURL, *certFile, *keyFile, *caFile, *serverName, *outbox,
		func(a approval.Approval) string { return a.SignalRef })
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	defer closer()

	if err := pub.Publish(context.Background(), "thump.approvals", a); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "approved %s as %s", a.SignalRef, a.Approver)
	return 0
}

func runForce(args []string, stdout, stderr io.Writer) int {
	usage := "usage: calipers force <fingerprint> [--operator name] [--inbox dir] [--outbox dir] [--nats-url url] [--tls-cert path --tls-key path --tls-ca path --server-name name]"
	fp, rest, ok := shiftPositional(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("force", flag.ContinueOnError)
	fs.SetOutput(stderr)
	operator := fs.String("operator", os.Getenv("USER"), "who is forcing this through")
	inbox := fs.String("inbox", ".", "directory calipers reads incidents from")
	outbox := fs.String("outbox", ".", "directory calipers writes the forced Governed to (thump.decisions in production)")
	natsURL := fs.String("nats-url", "", "publish to thump.decisions over NATS instead of --outbox")
	certFile := fs.String("tls-cert", "", "client cert, required with --nats-url")
	keyFile := fs.String("tls-key", "", "client key, required with --nats-url")
	caFile := fs.String("tls-ca", "", "CA bundle, required with --nats-url")
	serverName := fs.String("server-name", "", "TLS SAN to verify the peer against, if it differs from the dialed host (e.g. a port-forwarded nats-url)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	proj, closer, err := operatorProjection(context.Background(), *natsURL, *certFile, *keyFile, *caFile, *serverName, *inbox)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	defer closer()
	inc, ok := proj.Get(fp)
	if !ok || inc.Governed == nil || !inc.Governed.Decision.Verdict.AwaitsApproval() {
		_, _ = fmt.Fprintf(stderr, "calipers: %s is not currently held — nothing to force\n", fp)
		return 1
	}

	g := *inc.Governed
	g.Decision.ID = fmt.Sprintf("dec:%s:force:%d", fp, time.Now().Unix())
	g.Decision.Verdict = decision.VerdictApproved
	g.Decision.GrantedBand = g.Decision.RequestedBand
	g.Decision.Reasons = nil // the risk-ceiling reason that earned the hold no longer applies once a human overrides it
	g.Decision.Forced = true
	g.Decision.Operator = *operator
	g.Decision.EvaluatedAt = time.Now()

	if err := g.Decision.Auditable(); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}

	pub, closer, err := operatorPublisher(*natsURL, *certFile, *keyFile, *caFile, *serverName, *outbox,
		func(g decision.Governed) string { return g.Decision.SignalRef })
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	defer closer()

	if err := pub.Publish(context.Background(), "thump.decisions", g); err != nil {
		_, _ = fmt.Fprintln(stderr, "calipers:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "FORCED %s by %s — bypassed hiss's risk gate\n", fp, *operator)
	return 0
}
