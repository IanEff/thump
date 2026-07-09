package hiss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
)

func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	lc, code, exit := beat.Start("hiss", args, stdout, stderr, beat.Version{Version: version, Commit: commit, Date: date})
	if exit {
		return code
	}
	defer lc.Stop()
	ctx := lc.Ctx

	pol, err := loadPolicy(os.Getenv("HISS_POLICY"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "failed to load policy: %v\n", err)
		return 1
	}

	if lc.NATSURL != "" {
		return runBroker(ctx, lc.NATSURL, pol, stderr)
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
	beat.PollLoop(ctx, beat.PollConfig{Interval: 5 * time.Second}, tr.Tick)
	return 0
}

// runBroker is hiss's NATS branch: consume thump.proposals, evaluate
// authority, publish thump.decisions.
func runBroker(ctx context.Context, natsURL string, pol Policy, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	pub, closeW, err := beat.NewWALPublisher[decision.Governed](js, os.Getenv("WAL_DIR"), "hiss", "thump.decisions")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeW(ctx) }()

	tr := &Transport{Pub: pub, Policy: pol, Log: NewDecisionLog()}

	return beat.ExitOnError(ctx, beat.RunConsumer[proposal.Set](ctx, js, "thump.proposals", tr.handle))
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
