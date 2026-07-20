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

func runApprove(args []string, stdout, stderr io.Writer) int {
	usage := "usage: trim approve <fingerprint> [--approver name] [--outbox dir]"
	fp, rest, ok := shiftPositional(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	approver := fs.String("approver", os.Getenv("USER"), "who is approving")
	outbox := fs.String("outbox", ".", "directory trim writes the Approval to (thump.approvals in production)")
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

	pub := &publish.DirPublisher[approval.Approval]{
		Dir: *outbox,
		Name: func(a approval.Approval) string {
			return a.SignalRef
		},
	}
	if err := pub.Publish(context.Background(), "thump.approvals", a); err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "approved %s as %s", a.SignalRef, a.Approver)
	return 0
}

func runForce(args []string, stdout, stderr io.Writer) int {
	usage := "usage: trim force <fingerprint> [--operator name] [--inbox dir] [--outbox dir]"
	fp, rest, ok := shiftPositional(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}

	fs := flag.NewFlagSet("force", flag.ContinueOnError)
	fs.SetOutput(stderr)
	operator := fs.String("operator", os.Getenv("USER"), "who is forcing this through")
	inbox := fs.String("inbox", ".", "directory trim reads incidents from")
	outbox := fs.String("outbox", ".", "directory trim writes the forced Governed to (thump.decisions in production)")
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

	pub := &publish.DirPublisher[decision.Governed]{
		Dir:  *outbox,
		Name: func(g decision.Governed) string { return g.Decision.SignalRef },
	}
	if err := pub.Publish(context.Background(), "thump.decisions", g); err != nil {
		_, _ = fmt.Fprintln(stderr, "trim:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "FORCED %s by %s — bypassed hiss's risk gate\n", fp, *operator)
	return 0
}
