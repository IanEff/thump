package tracing_test

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ianeff/thump/internal/tracing"
)

// TestTraceIDFromFingerprint_IsValid pins that every fingerprint — including
// the empty string, which should never occur in production but shouldn't
// panic a span emitter if it ever does — mints a non-zero trace.TraceID. The
// OTel spec treats the all-zero ID as invalid; minting one would make the
// incident's root span silently undroppable-but-useless.
func TestTraceIDFromFingerprint_IsValid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		fingerprint string
	}{
		{"typical fingerprint", "checkout-svc/dependency_saturation/2026-07-09T00:00:00Z"},
		{"single character", "x"},
		{"empty fingerprint", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := tracing.TraceIDFromFingerprint(tc.fingerprint)
			if !id.IsValid() {
				t.Errorf("TraceIDFromFingerprint(%q).IsValid() = false, want true", tc.fingerprint)
			}
		})
	}
}

// TestTraceIDFromFingerprint_IsDeterministic pins the property B1 depends on:
// rattle, clank, hiss, and thump each derive the trace ID independently from
// the same signal.Detection.Fingerprint rather than passing one value hand to
// hand across JetStream — so the same fingerprint must mint the same ID every
// time, in every process.
func TestTraceIDFromFingerprint_IsDeterministic(t *testing.T) {
	t.Parallel()
	const fp = "checkout-svc/dependency_saturation/2026-07-09T00:00:00Z"
	first := tracing.TraceIDFromFingerprint(fp)
	second := tracing.TraceIDFromFingerprint(fp)
	if first != second {
		t.Errorf("TraceIDFromFingerprint(%q) = %v then %v, want the same ID both times", fp, first, second)
	}
}

// TestTraceIDFromFingerprint_DiffersByFingerprint pins that two distinct
// incidents never collide onto the same trace — a Tempo query for one
// fingerprint must never surface another incident's spans.
func TestTraceIDFromFingerprint_DiffersByFingerprint(t *testing.T) {
	t.Parallel()
	a := tracing.TraceIDFromFingerprint("checkout-svc/dependency_saturation/2026-07-09T00:00:00Z")
	b := tracing.TraceIDFromFingerprint("checkout-svc/dependency_saturation/2026-07-09T00:05:00Z")
	if a == b {
		t.Errorf("TraceIDFromFingerprint returned %v for two distinct fingerprints, want distinct IDs", a)
	}
}

// TestRootContext_MintsASpanSharingTheFingerprintsTraceID pins the property
// rattle's runLoop actually depends on: a tracer.Start on RootContext's
// returned context produces a span carrying
// TraceIDFromFingerprint(fingerprint) — proven through a real SDK tracer
// rather than by inspecting the seeded SpanContext's fields directly, since
// what matters is what Start does with the seed, not the seed's shape alone.
func TestRootContext_MintsASpanSharingTheFingerprintsTraceID(t *testing.T) {
	t.Parallel()
	const fp = "checkout-svc/dependency_saturation/2026-07-09T00:00:00Z"
	want := tracing.TraceIDFromFingerprint(fp)

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown tracer provider: %v", err)
		}
	})

	_, span := tp.Tracer("test").Start(tracing.RootContext(context.Background(), fp), "detect")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d recorded spans, want 1", len(spans))
	}
	if got := spans[0].SpanContext.TraceID(); got != want {
		t.Errorf("span trace_id = %s, want %s (tracing.TraceIDFromFingerprint(%q))", got, want, fp)
	}
}
