package beat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestStage_SuccessRecordsDurationButNoError pins the RED contract on the
// happy path: a stage that returns nil gets exactly one span (ended, not
// marked errored) and one duration observation — and must NOT touch the
// error counter, so a zero reading in Prometheus means zero errors, not a
// series that was simply never registered.
func TestStage_SuccessRecordsDurationButNoError(t *testing.T) {
	t.Parallel()
	exporter := &recordingExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	reg := prometheus.NewRegistry()
	rec := NewStageRecorder(reg)

	err := Stage(context.Background(), tp.Tracer("test"), rec, "assemble_sao", func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Stage returned %v, want nil", err)
	}

	if got := exporter.recorded(); got != 1 {
		t.Fatalf("exporter recorded %d spans, want 1", got)
	}
	if status := exporter.spans[0].Status(); status.Code == otelcodes.Error {
		t.Errorf("span status = %v, want a non-error status on a successful stage", status)
	}

	if got := testutil.CollectAndCount(rec.duration); got != 1 {
		t.Errorf("duration histogram got %d samples, want 1", got)
	}
	if got := testutil.CollectAndCount(rec.errors); got != 0 {
		t.Errorf("errors counter got %d series, want 0 (never incremented, never registered a label)", got)
	}
}

// TestStage_ErrorMarksSpanAndCountsError pins the failure path: the span is
// still ended (never leaked) but carries an error status, and the error
// counter — not just the duration histogram — records the failure, so a
// stage's error rate is a real Prometheus series, not something to infer
// from slog.
func TestStage_ErrorMarksSpanAndCountsError(t *testing.T) {
	t.Parallel()
	exporter := &recordingExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	reg := prometheus.NewRegistry()
	rec := NewStageRecorder(reg)

	boom := errors.New("boom")
	err := Stage(context.Background(), tp.Tracer("test"), rec, "llm_complete", func(context.Context) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Stage returned %v, want %v", err, boom)
	}

	if got := exporter.recorded(); got != 1 {
		t.Fatalf("exporter recorded %d spans, want 1 — a failed stage must still end its span", got)
	}
	if status := exporter.spans[0].Status(); status.Code != otelcodes.Error {
		t.Errorf("span status code = %v, want codes.Error", status.Code)
	}

	want := `
		# HELP stage_errors_total Count of a loop stage returning a non-nil error.
		# TYPE stage_errors_total counter
		stage_errors_total{stage="llm_complete"} 1
	`
	if err := testutil.CollectAndCompare(reg, strings.NewReader(want), "stage_errors_total"); err != nil {
		t.Error(err)
	}
}

// TestStage_NilRecorderStillSpansAndRuns pins the nil-safety every other
// seam in this package already promises (Tracer's noop fallback, Recorder's
// nil check in Click.Absorb) — a caller that never wired a StageRecorder
// must still get a real span and fn's result, not a panic.
func TestStage_NilRecorderStillSpansAndRuns(t *testing.T) {
	t.Parallel()
	exporter := &recordingExporter{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	called := false
	err := Stage(context.Background(), tp.Tracer("test"), nil, "gate_eval", func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Stage returned %v, want nil", err)
	}
	if !called {
		t.Fatal("fn was never called")
	}
	if got := exporter.recorded(); got != 1 {
		t.Fatalf("exporter recorded %d spans, want 1", got)
	}
}
