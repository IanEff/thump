package trim

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"
)

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
