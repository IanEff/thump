package beat_test

import (
	"testing"

	"github.com/ianeff/thump/internal/beat"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestTracerOrNoop_NilReturnsANoopTracer(t *testing.T) {
	t.Parallel()
	got := beat.TracerOrNoop(nil)
	if _, ok := got.(noop.Tracer); !ok {
		t.Errorf("a nil Tracer must default to noop.Tracer, got %T", got)
	}
}

func TestTracerOrNoop_NonNilPassesThrough(t *testing.T) {
	t.Parallel()
	want := noop.NewTracerProvider().Tracer("test")

	got := beat.TracerOrNoop(want)

	if got != want {
		t.Error("a non-nil Tracer must be returned unchanged")
	}
}
