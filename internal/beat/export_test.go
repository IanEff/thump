package beat

import (
	"context"
	"time"

	"github.com/ianeff/thump/internal/tlsx"
	"go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// NewOTLPExporterForTest exposes newOTLPExporter to beat_test without
// newOTLPExporter becoming part of beat's real API. Mirrors
// internal/hiss/export_test.go and internal/clank/export_test.go.
func NewOTLPExporterForTest(ctx context.Context, endpoint string, tlsCfg tlsx.Config) (sdktrace.SpanExporter, error) {
	return newOTLPExporter(ctx, endpoint, tlsCfg)
}

// ProbeIntervalForTest exposes probeInterval so beat_test can advance a fake
// clock past it without duplicating the constant.
const ProbeIntervalForTest = probeInterval

// NewBreakerExporterForTest builds a breakerExporter around next with an
// injectable clock, so beat_test can drive its open/closed transitions
// without a real collector or real sleeps.
func NewBreakerExporterForTest(next metric.Exporter, now func() time.Time) metric.Exporter {
	return &breakerExporter{next: next, now: now}
}

// PermanentGRPCFailureForTest exposes permanentGRPCFailure to beat_test.
func PermanentGRPCFailureForTest(err error) bool {
	return permanentGRPCFailure(err)
}
