package hiss_test

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/tracing"
)

// TestHandle_GovernSpanSharesTheProposalsTraceID pins hiss's half of B1: it
// never mints a trace (only rattle does that — internal/rattle/rattle_test.go),
// it just nests a "govern" span under whatever trace context already arrived
// on ctx from clank's publish. This test stands in for that arrival the same
// way the clank and broker tests do: a remote SpanContext seeded from
// tracing.TraceIDFromFingerprint.
func TestHandle_GovernSpanSharesTheProposalsTraceID(t *testing.T) {
	t.Parallel()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	ps := governedSet()
	want := tracing.TraceIDFromFingerprint(ps.SignalRef)
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    want,
		SpanID:     oteltrace.SpanID{1},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	ctx := oteltrace.ContextWithRemoteSpanContext(context.Background(), sc)

	tr := &hiss.Transport{Pub: &fakeDecisionPub{}, Policy: calmPolicy(), Log: hiss.NewDecisionLog(), Now: frozenNow}
	tr.Tracer = tp.Tracer("hiss")

	if err := tr.HandleForTest(ctx, ps, nil); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("handle produced no spans — was Transport.Tracer wired in?")
	}

	found := false
	for _, s := range spans {
		if s.Name != "govern" {
			continue
		}
		found = true
		if got := s.SpanContext.TraceID(); got != want {
			t.Errorf("govern span has trace_id %s, want %s (tracing.TraceIDFromFingerprint(%q))", got, want, ps.SignalRef)
		}
	}
	if !found {
		t.Error(`no span named "govern" — want one span around Authority.Evaluate`)
	}
}
