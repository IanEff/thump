package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ianeff/clank/internal/rattle"
)

// version information populated by ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Printf("rattle %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
		return
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.Info("starting rattle", "version", version, "commit", commit, "date", date)

	promURL := os.Getenv("PROM_URL")
	if promURL == "" {
		slog.Error("set PROM_URL")
		os.Exit(1)
	}
	r := &rattle.Reconciler{
		SLOs:     loadSLOs(),
		Source:   rattle.NewPromSource(promURL),
		Detector: rattle.AccelerationDetector{Threshold: 0.5},
		Debounce: rattle.NewDebouncer(10 * time.Minute),
	}

	ctx := context.Background()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for ; ; <-ticker.C {
		detections, err := r.Reconcile(ctx)
		if err != nil {
			slog.Error("reconcile failed", "error", err)
			continue
		}
		for _, d := range detections {
			slog.Info("detection",
				"name", d.Name,
				"fingerprint", d.Fingerprint,
				"detector", d.DetectorType,
				"accel", d.Divergence.Observed,
			)
		}
	}
}

func loadSLOs() []rattle.SLO {
	return []rattle.SLO{
		{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999, ContractRef: "ceph-rgw-availability:v1"},
	}
}
