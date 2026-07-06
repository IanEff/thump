// Rattle is the Detection layer of Thump's spicy five-layered DRAL dip.
package rattle

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	ossignal "os/signal"
	"syscall"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/whir"
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

	var topo TopologySource
	if catPath, sqPath := os.Getenv("WHIR_CATALOG"), os.Getenv("WHIR_STATE_QUERIES"); catPath != "" && sqPath != "" {
		queries, err := whir.LoadStateQueries(sqPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load state queries: %v\n", err)
			return 1
		}
		if _, err := whir.LoadCatalogFile(catPath); err != nil {
			_, _ = fmt.Fprintf(stderr, "load whir catalog: %v\n", err)
			return 1
		}
		topo = &WhirTopologySource{Resolver: &whir.Resolver{
			BaseURL: promURL,
			Client:  http.DefaultClient,
			Queries: queries,
		}}
	}

	var traffic TrafficSource
	if tqPath := os.Getenv("RATTLE_TRAFFIC"); tqPath != "" {
		queries, err := LoadTrafficQueries(tqPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load traffic queries: %v\n", err)
			return 1
		}
		traffic = &HubbleTrafficSource{BaseURL: promURL, Client: http.DefaultClient, Queries: queries}
	}

	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var pub publish.Publisher[signal.Detection]
	if natsURL := os.Getenv("NATS_URL"); natsURL != "" {
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
		w := &publish.WAL{Dir: walDir, Beat: "rattle", Subject: "thump.detections"}
		defer func() {
			_ = w.Close(ctx)
		}()
		pub = &publish.WALPublisher[signal.Detection]{
			WAL:  w,
			Next: publish.NewJetPublisher[signal.Detection](js),
		}
	} else if outbox := os.Getenv("RATTLE_OUTBOX"); outbox != "" {
		// offline path: the DirPublisher is now the keyless fake the seam
		// tests exercise — broker mode above is how this actually runs.
		if err := os.MkdirAll(outbox, 0o750); err != nil { //nolint:gosec
			_, _ = fmt.Fprintf(stderr, "mkdir outbox: %v\n", err)
			return 1
		}
		pub = &publish.DirPublisher[signal.Detection]{
			Dir:  outbox,
			Name: func(d signal.Detection) string { return d.Fingerprint },
		}
	}

	r := newReconciler(promURL, topo, traffic)
	runLoop(ctx, r, log, pub)
	return 0
}

// newReconciler assembles the Reconciler Main runs. Pulled out of Main so a
// test can drive it with a fake Source and prove the contract is actually
// wired — Main itself is only reachable with a live PROM_URL.
func newReconciler(promURL string, topo TopologySource, traffic TrafficSource) *Reconciler {
	return &Reconciler{
		SLOs:           loadSLOs(),
		Source:         NewPromSource(promURL),
		Detector:       AccelerationDetector{Threshold: 0.5},
		Sustained:      &SustainedBurnDetector{Threshold: 1.0, MinSamples: 5},
		Debounce:       NewDebouncer(10 * time.Minute),
		TopologySource: topo,
		TrafficSource:  traffic,
		Contract: &SignalContract{
			FreshnessBound:  5 * time.Minute, // samples land every 1m; >5m stale = scrape path is broken
			ConfidenceFloor: 0.5,             // attenuation never drives confidence below "suspect"
		},
	}
}

func runLoop(ctx context.Context, r *Reconciler, log *slog.Logger, pub publish.Publisher[signal.Detection]) {
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
				if pub != nil {
					if err := pub.Publish(ctx, "thump.detections", d); err != nil {
						log.Error("publish failed", "fingerprint", d.Fingerprint, "error", err)
					}
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// TODO(ian): STUB — hardcoded watch list.
func loadSLOs() []SLO {
	return []SLO{
		{
			ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-rgw-availability:v1",
			Dependencies: []Dependency{{Name: "cephobjectstore", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "ceph-osd-latency", Object: "ceph-osd", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "ceph-osd-latency:v1",
			Dependencies: []Dependency{{Name: "cephblockpool", Role: "blocking"}, {Name: "ceph-node-1", Role: "blocking"}, {Name: "ceph-node-2", Role: "blocking"}, {Name: "ceph-node-3", Role: "blocking"}},
		},
		{
			ID: "ceph-health", Object: "ceph-cluster", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-health:v1",
			Dependencies: []Dependency{{Name: "cephcluster", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "argocd-sync", Object: "argocd", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "argocd-sync:v1",
			Dependencies: []Dependency{{Name: "cilium", Role: "blocking"}, {Name: "rook-operator", Role: "optional"}},
		},
	}
}
