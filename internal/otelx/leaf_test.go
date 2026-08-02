package otelx_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestOtelxIsALeafPackage pins otelx's import boundary: stdlib, tlsx (trace
// dialing reuses the same *tls.Config builder every beat's Metrics does),
// the OTel trace SDK/API and its two otlp exporters (trace.go's Tracer),
// autoexport plus the OTel metrics SDK (otlpbreaker.go's breakerExporter,
// registered as the OTEL_METRICS_EXPORTER=otlp-breaker factory), and grpc's
// credentials/codes/status (TLS dial credentials and gRPC failure
// classification) — but never AWS, Prometheus, or NATS. Those belong to
// other beat concerns entirely; otelx is OTel plumbing only.
func TestOtelxIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		leaftest.Stdlib,
		"github.com/ianeff/thump/internal/tlsx",
		"go.opentelemetry.io/otel",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc",
		"go.opentelemetry.io/otel/sdk/resource",
		"go.opentelemetry.io/otel/sdk/trace",
		"go.opentelemetry.io/otel/semconv/v1.26.0",
		"go.opentelemetry.io/otel/trace",
		"go.opentelemetry.io/otel/trace/noop",
		"google.golang.org/grpc/credentials",
		"go.opentelemetry.io/contrib/exporters/autoexport",
		"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc",
		"go.opentelemetry.io/otel/sdk/metric",
		"go.opentelemetry.io/otel/sdk/metric/metricdata",
		"google.golang.org/grpc/codes",
		"google.golang.org/grpc/status",
	)
}
