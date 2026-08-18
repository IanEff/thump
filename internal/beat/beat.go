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

// Shutdown releases whatever a listener or tracer allocated — never nil, so a
// caller can unconditionally `defer shutdown(ctx)` even on an unconfigured
// path, with no nil check standing between every beat and the same
// one-liner. otelx.Tracer returns the bare func(context.Context) error
// rather than this named type — Go's function-type assignability makes the
// two interchangeable at every call site, so otelx never needs to import
// beat to hand one back.
type Shutdown func(context.Context) error

// Lifecycle is what Start hands back for the running (non-exit) path: the
// shutdown-aware context every beat loops on, the NATS URL that selects broker
// vs. offline mode ("" ⇒ offline dir-poll), the Once flag an offline-mode
// Main may honor to run a single Tick and return instead of poll.Loop-ing
// forever, and the Stop that releases the signal handler.
type Lifecycle struct {
	Ctx     context.Context
	NATSURL string
	Once    bool
	Stop    func()
}

// Start runs the preamble every beat's Main otherwise repeats: parse the
// standard --version and --once flags (printing the build stamps and asking
// Main to exit when --version is set), install the JSON slog default, wire a
// SIGINT/SIGTERM-cancelled context, and log "starting <name>". When exit is
// true Main should return code immediately; otherwise it proceeds with lc
// (and defers lc.Stop). --once is defined here rather than per-Main so every
// offline-mode beat gets the same flag name and help text; broker-mode Mains
// are free to ignore lc.Once, since it only ever meant something without a
// broker to keep a long-lived consumer running against.
func Start(name string, args []string, stdout, stderr io.Writer, v Version) (lc Lifecycle, code int, exit bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	printVersion := fs.Bool("version", false, "print version and exit")
	once := fs.Bool("once", false, "offline mode only: run a single poll pass and exit, instead of looping forever")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Lifecycle{}, 0, true
		}
		slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).With("beat", name).Error("failed to parse flags", "err", err)
		return Lifecycle{}, 1, true
	}

	if *printVersion {
		_, _ = fmt.Fprintf(stdout, "%s %s\ncommit: %s\nbuilt: %s\n", name, v.Version, v.Commit, v.Date)
		return Lifecycle{}, 0, true
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).With("beat", name))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	slog.Info("starting "+name, "version", v.Version, "commit", v.Commit, "date", v.Date)

	return Lifecycle{Ctx: ctx, NATSURL: os.Getenv("NATS_URL"), Once: *once, Stop: stop}, 0, false
}
