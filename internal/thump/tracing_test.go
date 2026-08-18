package thump_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/internal/thump"
	"github.com/ianeff/thump/internal/tracing"
)

// TestHandle_RenderAndExecuteSpansShareTheDecisionsTraceID pins that thump
// never mints a trace (only rattle does — internal/rattle/rattle_test.go) —
// it nests "render" and "execute" spans under whatever trace context arrived
// on ctx from hiss's publish.
func TestHandle_RenderAndExecuteSpansShareTheDecisionsTraceID(t *testing.T) {
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

	if err := tr.HandleForTest(ctx, g, nil); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("handle produced no spans — was Transport.Tracer wired in?")
	}

	gotNames := make(map[string]bool, len(spans))
	for _, s := range spans {
		gotNames[s.Name] = true
		if got := s.SpanContext.TraceID(); got != want {
			t.Errorf("span %q has trace_id %s, want %s (tracing.TraceIDFromFingerprint(%q))", s.Name, got, want, g.Decision.SignalRef)
		}
	}
	for _, stage := range []string{"render", "execute"} {
		if !gotNames[stage] {
			t.Errorf("no span named %q — want one span per stage", stage)
		}
	}
}

// TestWatchAndSettle_SpansShareTheDecisionsTraceID pins that the async convergence
// watch and any fired reversal undo inherit the decision's TraceID.
func TestWatchAndSettle_SpansShareTheDecisionsTraceID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exporter := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		defer func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				t.Fatalf("shutdown tracer provider: %v", err)
			}
		}()

		inbox, outbox := t.TempDir(), t.TempDir()
		writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())

		runner := &fakeRunner{}
		tr := newTestTransport(inbox, outbox)
		tr.Exec = thump.Live{Runner: runner}
		tr.Tracer = tp.Tracer("thump")
		tr.Reversal = &thump.ReversalWatcher{
			Probe: thump.PrometheusConverger{Probe: &fakeProbe{
				answer: false, severity: 0.8, severityOK: true,
			}},
			Now: frozenNow,
		}

		g := approvedGoverned()
		want := tracing.TraceIDFromFingerprint(g.Decision.SignalRef)
		sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
			TraceID:    want,
			SpanID:     oteltrace.SpanID{1},
			TraceFlags: oteltrace.FlagsSampled,
			Remote:     true,
		})
		ctx := oteltrace.ContextWithRemoteSpanContext(context.Background(), sc)

		if err := tr.HandleForTest(ctx, g, nil); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()                          // settle goroutine reaches its timer block
		time.Sleep(goldenOrder().Success.Window) // fake clock jumps the window
		synctest.Wait()                          // settle goroutine finishes post-window work

		spans := exporter.GetSpans()
		if len(spans) == 0 {
			t.Fatal("handle produced no spans — was Transport.Tracer wired in?")
		}

		gotNames := make(map[string]bool, len(spans))
		for _, s := range spans {
			gotNames[s.Name] = true
			if got := s.SpanContext.TraceID(); got != want {
				t.Errorf("span %q has trace_id %s, want %s (tracing.TraceIDFromFingerprint(%q))", s.Name, got, want, g.Decision.SignalRef)
			}
		}
		for _, stage := range []string{"render", "execute", "settle", "undo"} {
			if !gotNames[stage] {
				t.Errorf("no span named %q — want one span per stage", stage)
			}
		}
	})
}
