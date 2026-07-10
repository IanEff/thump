package beat

import (
	"context"
	"errors"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// recordingExporter is a minimal fake — unlike tracetest.InMemoryExporter its
// Shutdown does not clear what was recorded, so a test can assert on spans
// after driving the full newTracer contract (use the tracer, then shut it
// down) instead of having to peek at exporter internals mid-test.
type recordingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (r *recordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, spans...)
	return nil
}

func (r *recordingExporter) Shutdown(context.Context) error { return nil }

func (r *recordingExporter) recorded() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.spans)
}

// TestNewTracer pins newTracer's env-driven contract without ever dialing a
// real collector — the exporterFactory seam lets each case supply a fake in
// place of the real OTLP/gRPC dial Tracer wires in production. Three claims:
// an empty endpoint short-circuits to a noop tracer and never touches the
// factory; a configured endpoint whose factory fails surfaces that error,
// wrapped, with no usable tracer; a configured endpoint whose factory
// succeeds hands back a tracer that actually emits through the exporter it
// was given, not a disconnected noop.
func TestNewTracer(t *testing.T) {
	errBoom := errors.New("boom")
	emittedExporter := &recordingExporter{}

	cases := map[string]struct {
		endpoint string
		factory  exporterFactory
		check    func(t *testing.T, tr trace.Tracer, shutdown Shutdown, err error)
	}{
		"newTracer returns a noop tracer and never calls the factory when the endpoint is empty": {
			endpoint: "",
			factory: func(context.Context, string) (sdktrace.SpanExporter, error) {
				t.Fatal("exporter factory must not be called when the endpoint is unconfigured")
				return nil, nil
			},
			check: func(t *testing.T, tr trace.Tracer, shutdown Shutdown, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if shutdown == nil {
					t.Fatal("Shutdown must never be nil — callers defer it unconditionally")
				}
				if err := shutdown(context.Background()); err != nil {
					t.Errorf("no-op shutdown returned an error: %v", err)
				}
				if tr == nil {
					t.Fatal("expected a usable (noop) tracer, got nil")
				}
				_, span := tr.Start(context.Background(), "probe") // must not panic on a noop tracer
				span.End()
			},
		},
		"newTracer wraps the exporter factory's error when the endpoint is configured": {
			endpoint: "collector:4317",
			factory: func(context.Context, string) (sdktrace.SpanExporter, error) {
				return nil, errBoom
			},
			check: func(t *testing.T, tr trace.Tracer, shutdown Shutdown, err error) {
				if !errors.Is(err, errBoom) {
					t.Fatalf("newTracer err = %v, want wrapping %v", err, errBoom)
				}
				if tr != nil || shutdown != nil {
					t.Error("a factory error must not hand back a usable tracer or shutdown")
				}
			},
		},
		"newTracer returns a tracer that emits through the configured exporter": {
			endpoint: "collector:4317",
			factory: func(context.Context, string) (sdktrace.SpanExporter, error) {
				return emittedExporter, nil
			},
			check: func(t *testing.T, tr trace.Tracer, shutdown Shutdown, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				_, span := tr.Start(context.Background(), "probe")
				span.End()
				if err := shutdown(context.Background()); err != nil {
					t.Fatalf("shutdown returned an error: %v", err)
				}
				if got := emittedExporter.recorded(); got != 1 {
					t.Errorf("exporter recorded %d spans, want 1 — tracer isn't wired to the given exporter", got)
				}
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr, shutdown, err := newTracer(context.Background(), "test-beat", tc.endpoint, tc.factory)
			tc.check(t, tr, shutdown, err)
		})
	}
}
