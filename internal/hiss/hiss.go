package hiss

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

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
)

func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	fs := flag.NewFlagSet("hiss", flag.ExitOnError)
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
		_, _ = fmt.Fprintf(stdout, "hiss %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting hiss", "version", version, "commit", commit, "date", date)

	pol, err := loadPolicy(os.Getenv("HISS_POLICY"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "failed to load policy: %v\n", err)
		return 1
	}

	if natsURL := os.Getenv("NATS_URL"); natsURL != "" {
		return runBroker(ctx, natsURL, pol, stderr)
	}

	// offline path: the dir-glob Transport is now the keyless fake the seam
	// tests exercise — broker mode above is how this actually runs.
	inbox := os.Getenv("HISS_INBOX")
	if inbox == "" {
		_, _ = fmt.Fprintln(stderr, "HISS_INBOX is required")
		return 1
	}
	outbox := os.Getenv("HISS_OUTBOX")
	if outbox == "" {
		_, _ = fmt.Fprintln(stderr, "HISS_OUTBOX is required")
		return 1
	}

	tr := &Transport{
		Inbox: inbox,
		Pub: &publish.DirPublisher[decision.Governed]{
			Dir:  outbox,
			Name: func(g decision.Governed) string { return g.Decision.SignalRef },
		},
		Policy: pol,
		Log:    NewDecisionLog(),
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

// runBroker is hiss's NATS branch: consume thump.proposals, evaluate
// authority, publish thump.decisions — mirrors clank.runBroker's shape
// (internal/clank/clank.go:123).
func runBroker(ctx context.Context, natsURL string, pol Policy, stderr io.Writer) int {
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
	w := &publish.WAL{Dir: walDir, Beat: "hiss", Subject: "thump.decisions"}
	defer func() { _ = w.Close(ctx) }()

	tr := &Transport{
		Pub:    &publish.WALPublisher[decision.Governed]{WAL: w, Next: publish.NewJetPublisher[decision.Governed](js)},
		Policy: pol,
		Log:    NewDecisionLog(),
	}

	sub := broker.NewJetSubscriber[proposal.Set](js)
	if err := sub.Run(ctx, "thump.proposals", tr.handle); err != nil && ctx.Err() == nil {
		slog.Error("broker run failed", "err", err)
		return 1
	}
	return 0
}

// loadPolicy reads HISS_POLICY as a YAML file and unmarshals it into a
// Policy. A missing path, an unreadable file, and a malformed file all
// fail the same way: a governor that started with a zero-value Policy
// would fail *closed* (MaxBand empty everywhere ⇒ everything escalates)
// but silently — refusing to start and saying why beats that.
func loadPolicy(path string) (Policy, error) {
	if path == "" {
		return Policy{}, errors.New("HISS_POLICY is required")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config path, not user input
	if err != nil {
		return Policy{}, fmt.Errorf("read policy file: %w", err)
	}
	var pol Policy
	if err := yaml.Unmarshal(raw, &pol); err != nil {
		return Policy{}, fmt.Errorf("parse policy file: %w", err)
	}
	return pol, nil
}
