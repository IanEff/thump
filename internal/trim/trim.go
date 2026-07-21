package trim

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/publish"
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

// Main is trim's entry point: routing to subcommand, then
// either the machine (--json) or human (Lip Gloss) path over
// the same Projection.
// It returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: trim <incidents> [--json] [--inbox dir]")
		return 2
	}
	switch args[0] {
	case "incidents":
		return runIncidents(args[1:], stdout, stderr)
	case "approve":
		return runApprove(args[1:], stdout, stderr)
	case "force":
		return runForce(args[1:], stdout, stderr)
	case "sync":
		return runSync(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "trim: unknown command: %q\n", args[0])
		return 2
	}
}

func runIncidents(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("incidents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print incidents as JSON")
	inbox := fs.String("inbox", ".", "directory trim polls for boundary objects")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	tr := &Transport{Inbox: *inbox}
	proj, err := tr.Snapshot(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}

	incidents := proj.Snapshot()
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(incidents); err != nil {
			_, _ = fmt.Fprintln(stderr, "trim:", err)
			return 1
		}
		return 0
	}

	_, _ = fmt.Fprintln(stdout, renderIncidents(incidents, time.Now()))
	return 0
}

// outboxPublisher picks the Publisher runApprove/runForce write through:
// DirPublisher when natsURL is empty (the offline/testscript path, unchanged
// from before this existed), or a live JetPublisher when it's set — so an
// operator pointed at a NATS-backed rig can reach hiss's real
// thump.approvals subscriber (or thump's thump.decisions one) directly,
// instead of a directory nothing durable ever reads. The returned close
// func is a no-op in the DirPublisher case.
func outboxPublisher[T any](natsURL, outbox string, name func(T) string) (publish.Publisher[T], func(), error) {
	if natsURL == "" {
		return &publish.DirPublisher[T]{Dir: outbox, Name: name}, func() {}, nil
	}
	js, closeNC, err := broker.Connect(context.Background(), natsURL)
	if err != nil {
		return nil, nil, err
	}
	return publish.NewJetPublisher[T](js), closeNC, nil
}

// runSync connects to NATS and materializes the current stream state into
// --inbox as YAML, so runIncidents (unmodified, filesystem-only) has
// something to read on a rig where NATS, not a shared directory, is how the
// beats actually exchange boundary objects.
func runSync(args []string, stdout, stderr io.Writer) int {
	usage := "usage: trim sync [--nats-url url] [--inbox dir]"
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	natsURL := fs.String("nats-url", os.Getenv("NATS_URL"), "NATS server URL (defaults to $NATS_URL)")
	inbox := fs.String("inbox", ".", "directory to materialize boundary objects into")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *natsURL == "" {
		_, _ = fmt.Fprintln(stderr, usage+" (or set NATS_URL)")
		return 2
	}

	ctx := context.Background()
	js, closeNC, err := broker.Connect(ctx, *natsURL)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}
	defer closeNC()

	n, err := (&NATSSync{JS: js, Inbox: *inbox}).Run(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "synced %d object(s) into %s\n", n, *inbox)
	return 0
}

func runApprove(args []string, stdout, stderr io.Writer) int {
	usage := "usage: trim approve <fingerprint> [--approver name] [--outbox dir] [--nats-url url]"
	fp, rest, ok := shiftPositional(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	approver := fs.String("approver", os.Getenv("USER"), "who is approving")
	outbox := fs.String("outbox", ".", "directory trim writes the Approval to (ignored if --nats-url is set)")
	natsURL := fs.String("nats-url", os.Getenv("NATS_URL"), "NATS server URL (defaults to $NATS_URL); publishes straight to thump.approvals instead of --outbox")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	a := approval.Approval{SignalRef: fp, Approver: *approver, ApprovedAt: time.Now()}
	if err := a.Auditable(); err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}

	pub, closeNC, err := outboxPublisher(*natsURL, *outbox, func(a approval.Approval) string { return a.SignalRef })
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}
	defer closeNC()

	if err := pub.Publish(context.Background(), "thump.approvals", a); err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}

	// "published," not "approved": this only puts the Approval on thump.approvals.
	// hiss decides whether the fingerprint is actually held and grants (or rejects,
	// for a stale/unheld one) — that verdict lands async on thump.decisions, not here.
	_, _ = fmt.Fprintf(stdout, "published approval for %s as %s — async; watch 'trim incidents' or hiss logs for the grant\n", a.SignalRef, a.Approver)
	return 0
}

func runForce(args []string, stdout, stderr io.Writer) int {
	usage := "usage: trim force <fingerprint> [--operator name] [--inbox dir] [--outbox dir] [--nats-url url]"
	fp, rest, ok := shiftPositional(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("force", flag.ContinueOnError)
	fs.SetOutput(stderr)
	operator := fs.String("operator", os.Getenv("USER"), "who is forcing this through")
	inbox := fs.String("inbox", ".", "directory trim reads incidents from (run trim sync first on a NATS-backed rig)")
	outbox := fs.String("outbox", ".", "directory trim writes the forced Governed to (ignored if --nats-url is set)")
	natsURL := fs.String("nats-url", os.Getenv("NATS_URL"), "NATS server URL (defaults to $NATS_URL); publishes straight to thump.decisions instead of --outbox")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	tr := &Transport{Inbox: *inbox}
	proj, err := tr.Snapshot(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}
	inc, ok := proj.Get(fp)
	if !ok || inc.Held == nil {
		_, _ = fmt.Fprintf(stderr, "trim: %s is not currently held — nothing to force\n", fp)
		return 1
	}

	g := *inc.Held
	g.Decision.ID = fmt.Sprintf("dec:%s:force:%d", fp, time.Now().Unix())
	g.Decision.Verdict = decision.VerdictApproved
	g.Decision.GrantedBand = g.Decision.RequestedBand
	g.Decision.Reasons = nil // the risk-ceiling reason that earned the hold no longer applies once a human overrides it
	g.Decision.Forced = true
	g.Decision.Operator = *operator
	g.Decision.EvaluatedAt = time.Now()

	if err := g.Decision.Auditable(); err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}

	pub, closeNC, err := outboxPublisher(*natsURL, *outbox, func(g decision.Governed) string { return g.Decision.SignalRef })
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}
	defer closeNC()

	if err := pub.Publish(context.Background(), "thump.decisions", g); err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "FORCED %s by %s — bypassed hiss's risk gate\n", fp, *operator)
	return 0
}
