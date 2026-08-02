package beat_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ianeff/thump/internal/beat"
)

// countingExporter is a metric.Exporter test double that records how many
// times Export was called and always returns the same canned error — enough
// to prove whether breakerExporter skipped or forwarded a given attempt.
type countingExporter struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (c *countingExporter) Temporality(metric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (c *countingExporter) Aggregation(metric.InstrumentKind) metric.Aggregation {
	return metric.AggregationDefault{}
}

func (c *countingExporter) ForceFlush(context.Context) error { return nil }

func (c *countingExporter) Shutdown(context.Context) error { return nil }

func (c *countingExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.err
}

func (c *countingExporter) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// fakeClock is a mutable now() source so a test can cross probeInterval
// without a real sleep.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func TestPermanentGRPCFailure(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err  error
		want bool
	}{
		"permanentGRPCFailure classifies Unimplemented as permanent": {
			err:  status.Error(codes.Unimplemented, "unknown service opentelemetry.proto.collector.metrics.v1.MetricsService"),
			want: true,
		},
		"permanentGRPCFailure classifies Unavailable as not permanent": {
			err:  status.Error(codes.Unavailable, "collector restarting"),
			want: false,
		},
		"permanentGRPCFailure classifies a nil error as not permanent": {
			err:  nil,
			want: false,
		},
		"permanentGRPCFailure classifies DeadlineExceeded as not permanent": {
			err:  status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
			want: false,
		},
		"permanentGRPCFailure classifies PermissionDenied as permanent": {
			err:  status.Error(codes.PermissionDenied, "denied"),
			want: true,
		},
		"permanentGRPCFailure classifies a plain non-gRPC error as permanent": {
			err:  errors.New("boom"),
			want: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := beat.PermanentGRPCFailureForTest(tc.err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong permanence classification (-want +got)\n", diff)
			}
		})
	}
}

func TestBreakerExporterExport_OpensOnFirstPermanentFailureAndPropagatesTheError(t *testing.T) {
	t.Parallel()
	wantErr := status.Error(codes.Unimplemented, "no MetricsService")
	next := &countingExporter{err: wantErr}
	clock := &fakeClock{now: time.Unix(0, 0)}
	exp := beat.NewBreakerExporterForTest(next, clock.Now)

	err := exp.Export(context.Background(), &metricdata.ResourceMetrics{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Export err = %v, want wrapping %v", err, wantErr)
	}
	if got := next.callCount(); got != 1 {
		t.Errorf("wrapped exporter called %d times, want 1 — the first attempt must still go through", got)
	}
}

func TestBreakerExporterExport_SkipsTheWrappedExporterWhileOpenAndBeforeProbeTime(t *testing.T) {
	t.Parallel()
	next := &countingExporter{err: status.Error(codes.Unimplemented, "no MetricsService")}
	clock := &fakeClock{now: time.Unix(0, 0)}
	exp := beat.NewBreakerExporterForTest(next, clock.Now)

	if err := exp.Export(context.Background(), &metricdata.ResourceMetrics{}); err == nil {
		t.Fatal("first call must fail to open the breaker")
	}

	err := exp.Export(context.Background(), &metricdata.ResourceMetrics{})
	if err != nil {
		t.Errorf("Export err = %v, want nil — an open breaker short of its probe time must not surface an error", err)
	}
	if got := next.callCount(); got != 1 {
		t.Errorf("wrapped exporter called %d times, want 1 — the second attempt must be skipped entirely", got)
	}
}

func TestBreakerExporterExport_CallsTheWrappedExporterAgainAfterProbeIntervalElapses(t *testing.T) {
	t.Parallel()
	next := &countingExporter{err: status.Error(codes.Unimplemented, "no MetricsService")}
	clock := &fakeClock{now: time.Unix(0, 0)}
	exp := beat.NewBreakerExporterForTest(next, clock.Now)

	if err := exp.Export(context.Background(), &metricdata.ResourceMetrics{}); err == nil {
		t.Fatal("first call must fail to open the breaker")
	}

	clock.advance(beat.ProbeIntervalForTest)
	next.err = nil // the probe succeeds — endpoint fixed itself
	if err := exp.Export(context.Background(), &metricdata.ResourceMetrics{}); err != nil {
		t.Errorf("probe attempt returned %v, want nil", err)
	}
	if got := next.callCount(); got != 2 {
		t.Errorf("wrapped exporter called %d times, want 2 — advancing past probeInterval must trigger a real probe", got)
	}
}

func TestBreakerExporterExport_NeverOpensForTransientFailures(t *testing.T) {
	t.Parallel()
	next := &countingExporter{err: status.Error(codes.Unavailable, "collector restarting")}
	clock := &fakeClock{now: time.Unix(0, 0)}
	exp := beat.NewBreakerExporterForTest(next, clock.Now)

	for i := 1; i <= 3; i++ {
		err := exp.Export(context.Background(), &metricdata.ResourceMetrics{})
		if err == nil {
			t.Fatalf("call %d: want the transient error to propagate, got nil", i)
		}
	}
	if got := next.callCount(); got != 3 {
		t.Errorf("wrapped exporter called %d times, want 3 — a transient failure must never trip the breaker", got)
	}
}
