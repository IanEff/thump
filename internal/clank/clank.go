package clank

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

func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	fs := flag.NewFlagSet("clank", flag.ContinueOnError)
	fs.SetOutput(stdout)

	printVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "failed to parse flags: %v\n", err)
		return 1
	}

	if *printVersion {
		_, _ = fmt.Fprintf(stdout, "clank %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting clank", "version", version, "commit", commit, "date", date)

	<-ctx.Done()

	slog.Info("shutting down")
	return 0
}
