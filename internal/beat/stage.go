package beat

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// StageRecorder is the RED half of a loop stage's instrumentation — one
// duration histogram and one error counter, both keyed by stage name, so
// "assemble_sao p99" or "govern's error rate" are Prometheus queries, not a
// slog grep. Register it through the same beat="<name>"-wrapped Registerer
// beat.Metrics returns, so a stage metric never carries its own beat label.
type StageRecorder struct {
	duration *prometheus.HistogramVec
	errors   *prometheus.CounterVec
}

// NewStageRecorder registers a StageRecorder's two series through reg.
func NewStageRecorder(reg prometheus.Registerer) *StageRecorder {
	r := &StageRecorder{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "stage_duration_seconds",
			Help:    "How long one loop stage took, keyed by stage name.",
			Buckets: prometheus.DefBuckets,
		}, []string{"stage"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stage_errors_total",
			Help: "Count of a loop stage returning a non-nil error.",
		}, []string{"stage"}),
	}
	reg.MustRegister(r.duration, r.errors)
	return r
}

func (r *StageRecorder) observe(stage string, dur time.Duration, err error) {
	r.duration.WithLabelValues(stage).Observe(dur.Seconds())
	if err != nil {
		r.errors.WithLabelValues(stage).Inc()
	}
}

// Stage runs fn as one named loop stage: it opens a child span under tracer,
// times fn, records the duration — and, on error, the error — through rec,
// and logs one structured line per call. rec may be nil (a caller that never
// wired a StageRecorder still gets tracing and slog); tracer is never
// nil-checked here because every caller already resolves it through its own
// nil-safe tracer() accessor before reaching this function.
//
// This is the one seam every stage boundary in clank, hiss, and thump
// instruments through, so "how long did assemble_sao take" and "how long
// did govern take" are the same kind of question everywhere — not six
// hand-rolled variants of the same twelve lines.
func Stage(ctx context.Context, tracer trace.Tracer, rec *StageRecorder, name string, fn func(context.Context) error) error {
	sctx, span := tracer.Start(ctx, name)
	start := time.Now()
	err := fn(sctx)
	dur := time.Since(start)

	if rec != nil {
		rec.observe(name, dur, err)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.Error(name, "duration_ms", dur.Milliseconds(), "err", err)
	} else {
		slog.Info(name, "duration_ms", dur.Milliseconds())
	}
	span.End()
	return err
}
