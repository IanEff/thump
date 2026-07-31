package beat

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ianeff/thump/internal/tlsx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"
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
//
// The otelc auto-instrumentation layer (.otelc-build) reads this same var
// independently, through autoexport rather than otlpDialOptions below — it
// needs a full URL, not the bare host:port otlpDialOptions itself would
// accept, so the value must always carry a scheme.
func Tracer(ctx context.Context, beatName string, tlsCfg tlsx.Config) (trace.Tracer, Shutdown, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	factory := func(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
		return newOTLPExporter(ctx, endpoint, tlsCfg)
	}
	return newTracer(ctx, beatName, endpoint, factory)
}

func newTracer(ctx context.Context, beatName, endpoint string, newExporter exporterFactory) (trace.Tracer, Shutdown, error) {
	if endpoint == "" {
		return noop.Tracer{}, func(context.Context) error { return nil }, nil
	}
	if _, err := otlpDialOptions(endpoint); err != nil {
		return nil, nil, fmt.Errorf("beat: %w", err)
	}

	exp, err := newExporter(ctx, endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("beat: build span exporter for %q: %w", endpoint, err)
	}

	// Every beat's binary is copied into its image as the literal filename
	// "beat" (the Dockerfile's `COPY --from=build /out/${BEAT}
	// /usr/local/bin/beat`), so the SDK's own binary-name-derived default
	// resource would tag every beat's spans "unknown_service:beat" —
	// indistinguishable from one another in the trace backend. Overwrite
	// service.name with beatName explicitly so a query for "clank" or
	// "hiss" actually discriminates.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(semconv.ServiceNameKey.String(beatName)))
	if err != nil {
		return nil, nil, fmt.Errorf("beat: build resource for %q: %w", beatName, err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Tracer(beatName), tp.Shutdown, nil
}

func newOTLPExporter(ctx context.Context, endpoint string, tlsCfg tlsx.Config) (sdktrace.SpanExporter, error) {
	dial, err := otlpDialOptions(endpoint)
	if err != nil {
		return nil, err
	}
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(dial.Host)}
	if dial.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	} else {
		tc, err := tlsx.Client(tlsCfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tc)))
	}
	return otlptracegrpc.New(ctx, opts...)
}

// otlpDial is what one OTEL_EXPORTER_OTLP_ENDPOINT value resolves to:
// the host:port the SDK dials and whether the wire's in plaintext.
type otlpDial struct {
	Host     string
	Insecure bool
}

func otlpDialOptions(endpoint string) (otlpDial, error) {
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		return otlpDial{Host: strings.TrimPrefix(endpoint, "https://")}, nil
	case strings.HasPrefix(endpoint, "http://"):
		return otlpDial{Host: strings.TrimPrefix(endpoint, "http://"), Insecure: true}, nil
	case strings.Contains(endpoint, "://"):
		scheme, _, _ := strings.Cut(endpoint, "://")
		return otlpDial{}, fmt.Errorf("beat %q: unmappable OTLP scheme %q, want http or https", endpoint, scheme)
	default:
		return otlpDial{Host: endpoint, Insecure: true}, nil
	}
}
