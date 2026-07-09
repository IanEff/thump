// Package beat is the runtime kit every thump beat's Main composes: the
// process lifecycle (flags, logging, signal-driven shutdown) plus the two
// transports a beat runs on — the NATS consumer/publisher and the offline
// directory poll. It knows nothing about any plane's domain types; a beat
// supplies its own handler and wiring. The kit imports only the shared
// transport infrastructure (broker/publish/wire) — never another beat — an
// invariant pinned by leaf_test.go, so the kit can never become a place where
// the planes mash together.
package beat

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
)

// Version carries the ldflag-injected build stamps every beat prints for
// --version.
type Version struct {
	Version, Commit, Date string
}

// Lifecycle is what Start hands back for the running (non-exit) path: the
// shutdown-aware context every beat loops on, the NATS URL that selects broker
// vs. offline mode ("" ⇒ offline dir-poll), and the Stop that releases the
// signal handler.
type Lifecycle struct {
	Ctx     context.Context
	NATSURL string
	Stop    func()
}

// Start runs the preamble every beat's Main otherwise repeats: parse the
// standard --version flag (printing the build stamps and asking Main to exit
// when set), install the JSON slog default, wire a SIGINT/SIGTERM-cancelled
// context, and log "starting <name>". When exit is true Main should return
// code immediately; otherwise it proceeds with lc (and defers lc.Stop).
func Start(name string, args []string, stdout, stderr io.Writer, v Version) (lc Lifecycle, code int, exit bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	printVersion := fs.Bool("version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Lifecycle{}, 0, true
		}
		_, _ = fmt.Fprintf(stderr, "failed to parse flags: %v\n", err)
		return Lifecycle{}, 1, true
	}

	if *printVersion {
		_, _ = fmt.Fprintf(stdout, "%s %s\ncommit: %s\nbuilt: %s\n", name, v.Version, v.Commit, v.Date)
		return Lifecycle{}, 0, true
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	slog.Info("starting "+name, "version", v.Version, "commit", v.Commit, "date", v.Date)

	return Lifecycle{Ctx: ctx, NATSURL: os.Getenv("NATS_URL"), Stop: stop}, 0, false
}
