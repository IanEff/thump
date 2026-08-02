package beat

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ianeff/thump/internal/health"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsReadHeaderTimeout bounds how long the /metrics server waits for a
// scraper's request headers — unbounded here is a Slowloris opening, not a
// scraper's actual behavior.
const metricsReadHeaderTimeout = 5 * time.Second

// Metrics builds a fresh Registry, wrapped so every metric registered
// through the returned Registerer carries a beat="<beatName>" label without
// each beat's own metric declarations having to add it themselves. It serves
// /metrics on METRICS_ADDR — TLS via tlsCfg when non-nil, plaintext
// otherwise — and /healthz+/readyz on a separate HEALTH_ADDR that is always
// plaintext: kubelet's httpGet probes carry no client certificate, so they
// can never pass tlsCfg's mTLS requirement, and splitting the health
// listener out is what lets a probe and an mTLS-only scrape endpoint coexist
// on one pod. Either address empty is a valid unconfigured state — same
// "noop is a valid production state" discipline as Tracer.
func Metrics(beatName string, tlsCfg *tls.Config) (prometheus.Registerer, *health.Health, Shutdown) {
	reg := prometheus.NewRegistry()
	wrapped := prometheus.WrapRegistererWith(prometheus.Labels{"beat": beatName}, reg)
	h := &health.Health{}

	var shutdowns []Shutdown

	if addr := os.Getenv("METRICS_ADDR"); addr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		srv := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: metricsReadHeaderTimeout,
			TLSConfig:         tlsCfg,
		}

		serve := srv.ListenAndServe
		if tlsCfg != nil {
			serve = func() error { return srv.ListenAndServeTLS("", "") }
		}

		go func() {
			if err := serve(); !listenerStopWasClean(err) {
				slog.Error("metrics listener stopped", "addr", addr, "err", err)
			}
		}()
		shutdowns = append(shutdowns, srv.Shutdown)
	}

	if addr := os.Getenv("HEALTH_ADDR"); addr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", h.Livez)
		mux.HandleFunc("/readyz", h.Readyz)
		srv := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: metricsReadHeaderTimeout,
		}

		go func() {
			if err := srv.ListenAndServe(); !listenerStopWasClean(err) {
				slog.Error("health listener stopped", "addr", addr, "err", err)
			}
		}()
		shutdowns = append(shutdowns, srv.Shutdown)
	}

	return wrapped, h, joinShutdowns(shutdowns)
}

// joinShutdowns runs every listener's Shutdown and reports every failure
// among them — one failing to close must not hide another's error.
func joinShutdowns(shutdowns []Shutdown) Shutdown {
	return func(ctx context.Context) error {
		var errs []error
		for _, s := range shutdowns {
			if err := s(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
}

// listenerStopWasClean is kind of a hacky way of reporting whether
// the return was from a deliberate shutdown.
func listenerStopWasClean(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed)
}
