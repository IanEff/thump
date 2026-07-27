package beat

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
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
// serves it over METRICS_ADDR (":9090" style). Empty means unconfigured —
// same "noop is a valid production state" discipline as Tracer: a beat with
// no scraper pointed at it still runs, it just has nothing to collect into.
// A nil tlsCfg serves plaintext, the offline path's only option; a non-nil
// one — built by the caller via tlsx.Server, never here, so this package
// stays transport-only — serves ListenAndServeTLS("", ""), the empty
// strings because the certificate comes from tlsCfg's own GetCertificate
// callback, not a path this func would reread.
func Metrics(beatName string, tlsCfg *tls.Config) (prometheus.Registerer, *Health, Shutdown) {
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
	return wrapped, health, srv.Shutdown
}

// listenerStopWasClean is kind of a hacky way of reporting whether
// the return was from a deliberate shutdown.
func listenerStopWasClean(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed)
}
