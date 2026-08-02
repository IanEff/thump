package otelx

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
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
		check    func(t *testing.T, tr trace.Tracer, shutdown func(context.Context) error, err error)
	}{
		"newTracer returns a noop tracer and never calls the factory when the endpoint is empty": {
			endpoint: "",
			factory: func(context.Context, string) (sdktrace.SpanExporter, error) {
				t.Fatal("exporter factory must not be called when the endpoint is unconfigured")
				return nil, nil
			},
			check: func(t *testing.T, tr trace.Tracer, shutdown func(context.Context) error, err error) {
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
			check: func(t *testing.T, tr trace.Tracer, shutdown func(context.Context) error, err error) {
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
			check: func(t *testing.T, tr trace.Tracer, shutdown func(context.Context) error, err error) {
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

// TestNewTracer_TagsSpansWithTheBeatsServiceName pins the fix for a real
// deploy gap: every beat's binary is copied into its image as the literal
// filename "beat" (Dockerfile's `COPY --from=build /out/${BEAT}
// /usr/local/bin/beat`), so the OTel SDK's default resource detection —
// which derives service.name from the process's own binary name absent an
// explicit Resource — would tag every beat's spans "unknown_service:beat",
// indistinguishable from one another in Tempo. newTracer must set
// service.name from beatName explicitly so a query for "clank" or "hiss"
// actually discriminates.
func TestNewTracer_TagsSpansWithTheBeatsServiceName(t *testing.T) {
	exp := &recordingExporter{}
	tr, shutdown, err := newTracer(context.Background(), "clank", "collector:4317", func(context.Context, string) (sdktrace.SpanExporter, error) {
		return exp, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, span := tr.Start(context.Background(), "probe")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned an error: %v", err)
	}

	exp.mu.Lock()
	defer exp.mu.Unlock()
	if len(exp.spans) != 1 {
		t.Fatalf("want exactly 1 recorded span, got %d", len(exp.spans))
	}

	res := exp.spans[0].Resource()
	got, ok := res.Set().Value(semconv.ServiceNameKey)
	if !ok {
		t.Fatalf("recorded span's Resource carries no service.name attribute at all: %v", res)
	}
	if diff := cmp.Diff("clank", got.AsString()); diff != "" {
		t.Error("recorded span's service.name must be the beatName newTracer was given (-want +got)\n", diff)
	}
}

// failingExporter is an exporterFactory that panics if invoked — for cases
// where newTracer is supposed to refuse before any exporter gets built, so a
// dial being attempted at all is the failure, not a wrong return value.
func failingExporter(context.Context, string) (sdktrace.SpanExporter, error) {
	panic("exporter factory must not be called for an endpoint whose scheme can't be resolved")
}

func TestOTLPDialOptions_ReadsTheWireFromTheEndpointScheme(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		endpoint string
		want     otlpDial
		wantErr  bool
	}{
		"otlpDialOptions keeps the plaintext default for the bare host:port the chart ships": {
			endpoint: "tempo.tracing.svc.cluster.local:4317",
			want:     otlpDial{Host: "tempo.tracing.svc.cluster.local:4317", Insecure: true},
		},
		"otlpDialOptions strips an https scheme and asks for TLS": {
			endpoint: "https://tempo.tracing.svc.cluster.local:4317",
			want:     otlpDial{Host: "tempo.tracing.svc.cluster.local:4317"},
		},
		"otlpDialOptions strips an http scheme and stays plaintext": {
			endpoint: "http://tempo.tracing.svc.cluster.local:4317",
			want:     otlpDial{Host: "tempo.tracing.svc.cluster.local:4317", Insecure: true},
		},
		"otlpDialOptions refuses a scheme it cannot map to a wire rather than guessing": {
			endpoint: "grpcs://tempo.tracing.svc.cluster.local:4317",
			wantErr:  true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := otlpDialOptions(tc.endpoint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("an unmappable scheme must refuse, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong wire chosen for the endpoint", diff)
			}
		})
	}
}

func TestNewTracer_RefusesAnUnmappableEndpointInsteadOfSilentlyGoingNoop(t *testing.T) {
	t.Parallel()
	// An unconfigured endpoint is a legal production state and gets a noop
	// (see the empty-endpoint case above). A *configured but unusable* one is
	// a deploy error, and collapsing the two would hide a typo'd scheme as
	// "tracing is off".
	_, _, err := newTracer(context.Background(), "clank", "grpcs://tempo:4317", failingExporter)
	if err == nil {
		t.Error("a configured-but-unmappable endpoint must fail the beat, not degrade to noop")
	}
}
