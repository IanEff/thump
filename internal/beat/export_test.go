package beat

import (
	"context"

	"github.com/ianeff/thump/internal/tlsx"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// NewOTLPExporterForTest exposes newOTLPExporter to beat_test without
// newOTLPExporter becoming part of beat's real API. Mirrors
// internal/hiss/export_test.go and internal/clank/export_test.go.
func NewOTLPExporterForTest(ctx context.Context, endpoint string, tlsCfg tlsx.Config) (sdktrace.SpanExporter, error) {
	return newOTLPExporter(ctx, endpoint, tlsCfg)
}
