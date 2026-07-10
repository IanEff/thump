package clank_test

import (
	"context"
	"encoding/json"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/tracing"
)

// TestPropose_SpansShareTheDetectionsTraceID pins B1: one signal.Detection is
// one distributed trace. In production the trace context arrives already on
// ctx — rattle mints it from the Fingerprint and propagates it over JetStream
// headers before clank's transport ever calls Propose; Propose itself never
// forces a TraceID. This test stands in for that hand-off the same way: a
// remote SpanContext seeded from tracing.TraceIDFromFingerprint, exactly what
// extracting the propagated context off the wire would produce.
func TestPropose_SpansShareTheDetectionsTraceID(t *testing.T) {
	t.Parallel()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	}()

	model := &fakeModel{script: []clank.Completion{
		// turn 1: gather live evidence
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		// turn 2: propose
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}

	e, _ := newTestEngine(model)
	e.Tracer = tp.Tracer("clank")

	sig := sigBurnAccel()
	want := tracing.TraceIDFromFingerprint(sig.Fingerprint)
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    want,
		SpanID:     oteltrace.SpanID{1},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
	ctx := oteltrace.ContextWithRemoteSpanContext(context.Background(), sc)

	if _, err := e.Propose(ctx, sig); err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("Propose produced no spans — was Engine.Tracer wired in?")
	}

	gotNames := make(map[string]bool, len(spans))
	for _, s := range spans {
		gotNames[s.Name] = true
		if got := s.SpanContext.TraceID(); got != want {
			t.Errorf("span %q has trace_id %s, want %s (tracing.TraceIDFromFingerprint(%q))",
				s.Name, got, want, sig.Fingerprint)
		}
	}

	for _, stage := range []string{"assemble_sao", "llm_complete", "causal_score", "gate_eval"} {
		if !gotNames[stage] {
			t.Errorf("no span named %q — want one span per reason-loop stage", stage)
		}
	}
}
