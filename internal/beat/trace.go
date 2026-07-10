package beat

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Shutdown releases whatever Tracer allocated — never nil, so a caller can
// unconditionally `defer shutdown(ctx)` even on the unconfigured path, with
// no nil check standing between every beat and the same one-liner.
type Shutdown func(context.Context) error

// exporterFactory builds the span exporter newTracer batches through once an
// endpoint is configured — the seam that lets tests supply a fake exporter
// instead of dialing a real collector.
type exporterFactory func(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error)

// Tracer builds the tracer a beat's Engine/Transport spans through, reading
// OTEL_EXPORTER_OTLP_ENDPOINT from the environment. Empty means unconfigured:
// a beat run off-cluster, or in CI, gets a noop.Tracer rather than failing to
// start for want of a collector. A configured endpoint gets a batching
// OTLP/gRPC exporter, and the resulting provider is registered as otel's
// process-global default — internal/broker's and internal/publish's
// propagation.TraceContext{} read that global, so they need no wiring of
// their own.
func Tracer(ctx context.Context, beatName string) (trace.Tracer, Shutdown, error) {
	return newTracer(ctx, beatName, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), newOTLPExporter)
}

func newTracer(ctx context.Context, beatName, endpoint string, newExporter exporterFactory) (trace.Tracer, Shutdown, error) {
	if endpoint == "" {
		return noop.Tracer{}, func(context.Context) error { return nil }, nil
	}

	exp, err := newExporter(ctx, endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("beat: build span exporter for %q: %w", endpoint, err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	otel.SetTracerProvider(tp)
	return tp.Tracer(beatName), tp.Shutdown, nil
}

func newOTLPExporter(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
	return otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
}
