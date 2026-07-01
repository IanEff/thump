package rattle

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	ossignal "os/signal"
	"syscall"
	"time"
)

func Main(args []string, stdout, stderr io.Writer, version, commit, date string) int {
	fs := flag.NewFlagSet("rattle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	printVesion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *printVesion {
		_, _ = fmt.Fprintf(stdout, "rattle %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		return 0
	}

	log := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("starting rattle", "version", version, "commit", commit, "date", date)

	promURL := os.Getenv("PROM_URL")
	if promURL == "" {
		_, _ = fmt.Fprintln(stderr, "set PROM_URL")
		return 1
	}

	r := &Reconciler{
		SLOs:     loadSLOs(),
		Source:   NewPromSource(promURL),
		Detector: AccelerationDetector{Threshold: 0.5},
		Debounce: NewDebouncer(10 * time.Minute),
	}
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runLoop(ctx, r, log)
	return 0
}

func runLoop(ctx context.Context, r *Reconciler, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		detections, err := r.Reconcile(ctx)
		if err != nil {
			log.Error("reconcile failed", "error", err)
		} else {
			for _, d := range detections {
				log.Info("detection",
					"name", d.Name,
					"fingerprint", d.Fingerprint,
					"detector", d.DetectorType,
					"accel", d.Divergence.Observed)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// TODO(ian): STUB — hardcoded watch list. Replace with a real config/CRD source
// before this goes anywhere near production. See the vault guide's deferred list,
// "SLO config loading": declared Go value now, swap the source behind it later.
func loadSLOs() []SLO {
	return []SLO{
		{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999, ContractRef: "ceph-rgw-availability:v1"},
	}
}
