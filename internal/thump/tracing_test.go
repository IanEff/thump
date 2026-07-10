package thump_test

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/internal/thump"
	"github.com/ianeff/thump/internal/tracing"
)

// TestHandle_RenderSpanSharesTheDecisionsTraceID pins thump's half of B1: it
// never mints a trace (only rattle does — internal/rattle/rattle_test.go),
// it just nests a "render" span under whatever trace context already arrived
// on ctx from hiss's publish. Same stand-in trick as the clank and hiss
// tests: a remote SpanContext seeded from tracing.TraceIDFromFingerprint.
func TestHandle_RenderSpanSharesTheDecisionsTraceID(t *testing.T) {
	t.Parallel()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	g := approvedGoverned()
	want := tracing.TraceIDFromFingerprint(g.Decision.SignalRef)
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    want,
		SpanID:     oteltrace.SpanID{1},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	ctx := oteltrace.ContextWithRemoteSpanContext(context.Background(), sc)

	tr := &thump.Transport{
		OrderPub:   &fakeOrderPub{},
		OutcomePub: &fakeOutcomePub{},
		Catalog:    richCatalog(),
		Log:        thump.NewOutcomeLog(),
		Exec:       thump.DryRun{},
		Now:        frozenNow,
	}
	tr.Tracer = tp.Tracer("thump")

	if err := tr.HandleForTest(ctx, g); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("handle produced no spans — was Transport.Tracer wired in?")
	}

	found := false
	for _, s := range spans {
		if s.Name != "render" {
			continue
		}
		found = true
		if got := s.SpanContext.TraceID(); got != want {
			t.Errorf("render span has trace_id %s, want %s (tracing.TraceIDFromFingerprint(%q))", got, want, g.Decision.SignalRef)
		}
	}
	if !found {
		t.Error(`no span named "render" — want one span around Actuator.Render`)
	}
}
