package beat

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	autoexport.RegisterMetricReader("otlp-breaker", newBreakerMetricReader)
}

// probeInterval bounds how long breakerExporter waits before retrying an
// endpoint it has already proven can't accept OTLP metrics — long enough not
// to hammer a collector that returned a permanent failure, short enough that
// a rig-side fix lands within one probe without a pod restart.
const probeInterval = 5 * time.Minute

// newBreakerMetricReader is the OTEL_METRICS_EXPORTER=otlp-breaker factory:
// the same otlpmetricgrpc.New(ctx) autoexport's own "otlp" registration
// calls, wrapped in a breakerExporter so a permanently-unimplemented
// MetricsService stops the periodic reader from retrying and logging every
// collection interval forever.
func newBreakerMetricReader(ctx context.Context) (metric.Reader, error) {
	exp, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, err
	}
	return metric.NewPeriodicReader(&breakerExporter{next: exp}), nil
}

// breakerExporter wraps a metric.Exporter and, once next.Export proves the
// endpoint's failure is permanent (see permanentGRPCFailure), skips calling
// next entirely until probeInterval passes — the periodic reader's own
// ticker keeps firing every collection interval, but Export short-circuits
// to nil instead of dialing out and reporting the same failure again.
type breakerExporter struct {
	next metric.Exporter
	now  func() time.Time // overridden in tests; nil means time.Now

	mu        sync.Mutex
	open      bool
	nextProbe time.Time
}

func (b *breakerExporter) Temporality(k metric.InstrumentKind) metricdata.Temporality {
	return b.next.Temporality(k)
}

func (b *breakerExporter) Aggregation(k metric.InstrumentKind) metric.Aggregation {
	return b.next.Aggregation(k)
}

func (b *breakerExporter) ForceFlush(ctx context.Context) error { return b.next.ForceFlush(ctx) }

func (b *breakerExporter) Shutdown(ctx context.Context) error { return b.next.Shutdown(ctx) }

// Export skips next.Export and returns nil while the breaker is open and
// short of its next probe time — a nil return means the SDK's own
// error-reporting path never fires for a skipped attempt, which is what
// stops the log spam. Otherwise it calls through to next and updates the
// breaker from the result: success closes it, a permanent failure opens it
// (logged only on the transition), and a transient failure leaves it as-is.
func (b *breakerExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	now := b.clock()

	b.mu.Lock()
	if b.open && now.Before(b.nextProbe) {
		b.mu.Unlock()
		return nil
	}
	wasOpen := b.open
	b.mu.Unlock()

	err := b.next.Export(ctx, rm)

	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case err == nil:
		if wasOpen {
			slog.Info("metrics exporter recovered, closing breaker")
		}
		b.open = false
	case permanentGRPCFailure(err):
		if !b.open {
			slog.Warn("metrics exporter hit a permanent failure, opening breaker",
				"error", err, "probe_interval", probeInterval)
		}
		b.open = true
		b.nextProbe = b.clock().Add(probeInterval)
	}
	return err
}

func (b *breakerExporter) clock() time.Time {
	if b.now == nil {
		return time.Now()
	}
	return b.now()
}

// permanentGRPCFailure reports whether err is a gRPC status code that will
// never resolve on its own — codes.Unimplemented chief among them, since a
// server with no MetricsService registered will never grow one by being
// retried. Mirrors otlpmetricgrpc's own internal retry classification: the
// codes it retries (Canceled, DeadlineExceeded, Aborted, OutOfRange,
// Unavailable, DataLoss, ResourceExhausted) plus OK are not permanent;
// everything else is, including a non-gRPC err, which status.Code maps to
// codes.Unknown.
func permanentGRPCFailure(err error) bool {
	switch status.Code(err) {
	case codes.Canceled, codes.DeadlineExceeded, codes.Aborted, codes.OutOfRange,
		codes.Unavailable, codes.DataLoss, codes.ResourceExhausted, codes.OK:
		return false
	default:
		return true
	}
}
