package beat

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsReadHeaderTimeout bounds how long the /metrics server waits for a
// scraper's request headers — unbounded here is a Slowloris opening, not a
// scraper's actual behavior.
const metricsReadHeaderTimeout = 5 * time.Second

// Metrics builds a fresh Registry, wrapped so every metric registered
// through the returned Registerer carries a beat="<beatName>" label without
// each beat's own metric declarations having to add it themselves, and
// serves it over HTTP on METRICS_ADDR (":9090" style). Empty means
// unconfigured — same "noop is a valid production state" discipline as
// Tracer: a beat with no scraper pointed at it still runs, it just has
// nothing to collect into.
func Metrics(beatName string) (prometheus.Registerer, *Health, Shutdown) {
	reg := prometheus.NewRegistry()
	wrapped := prometheus.WrapRegistererWith(prometheus.Labels{"beat": beatName}, reg)
	health := &Health{}

	addr := os.Getenv("METRICS_ADDR")
	if addr == "" {
		return wrapped, health, func(context.Context) error { return nil }
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", health.Livez)
	mux.HandleFunc("/readyz", health.Readyz)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: metricsReadHeaderTimeout,
	}
	go func() {
		_ = srv.ListenAndServe()
	}()
	return wrapped, health, srv.Shutdown
}
