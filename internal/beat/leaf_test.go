package beat_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestBeatImportsNoBeat pins the kit's load-bearing invariant: internal/beat
// may import stdlib, the shared transport infrastructure (broker, publish,
// and the jetstream types they surface), health (consumer.go's BrokerHooks
// and AwaitConsumers take a *health.Health, and metrics.go's Metrics builds
// one), poll (objectstore.go's RunShipper drives its ship cadence through
// poll.Loop, the same offline ticker every beat's dir-poll Tick uses),
// tlsx (trace.go's Tracer and
// metrics.go's Metrics both build a *tls.Config from it rather than
// constructing one inline), the OTel tracing SDK plus grpc/credentials
// (trace.go's Tracer, which every beat's Main calls to build its span
// provider — credentials.NewTLS is the adapter from tlsx's *tls.Config to
// the credentials.TransportCredentials otlptracegrpc.WithTLSCredentials
// requires — and stage.go's Stage, which every beat's loop stages run
// through), the OTel metrics SDK plus autoexport and grpc/codes+status
// (otlpbreaker.go's breakerExporter, which registers as the
// OTEL_METRICS_EXPORTER=otlp-breaker factory autoexport's compile-time
// instrumentation layer selects, and classifies gRPC failures from the
// wrapped otlpmetricgrpc exporter to decide whether to trip), the Prometheus
// client (metrics.go's Metrics, stage.go's StageRecorder), the AWS SDK plus
// its underlying smithy-go transport (objectstore.go's NewS3SegmentSink,
// which builds the S3 client a WAL ships sealed segments through, and the
// finalize middleware it installs to work around a GCS signing quirk),
// sealbox (objectstore.go's EncryptingSink, which seals a segment before
// NewS3SegmentSink's inner sink ever sees it), and sigs.k8s.io/yaml
// (drain.go's DrainDir, which every beat's dir-poll Tick unmarshals its
// inbox files through) — but NEVER a beat package.
// A clank, rattle, hiss, or thump import appearing here means the runtime
// kit has become a place where the planes mash together; this test is that
// regression's tripwire. Widen the allowlist below when tracing, metrics,
// or the object store grows a new dependency; never widen it with a beat
// import.
func TestBeatImportsNoBeat(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		leaftest.Stdlib,
		"github.com/ianeff/thump/internal/broker",
		"github.com/ianeff/thump/internal/health",
		"github.com/ianeff/thump/internal/poll",
		"github.com/ianeff/thump/internal/publish",
		"github.com/nats-io/nats.go/jetstream",
		"github.com/prometheus/client_golang/prometheus",
		"github.com/prometheus/client_golang/prometheus/promhttp",
		"go.opentelemetry.io/otel",
		"go.opentelemetry.io/otel/codes",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc",
		"go.opentelemetry.io/otel/sdk/resource",
		"go.opentelemetry.io/otel/sdk/trace",
		"go.opentelemetry.io/otel/semconv/v1.26.0",
		"go.opentelemetry.io/otel/trace",
		"go.opentelemetry.io/otel/trace/noop",
		"github.com/aws/aws-sdk-go-v2/aws",
		"github.com/aws/aws-sdk-go-v2/config",
		"github.com/aws/aws-sdk-go-v2/credentials",
		"github.com/aws/aws-sdk-go-v2/service/s3",
		"github.com/aws/smithy-go/middleware",
		"github.com/aws/smithy-go/transport/http",
		"github.com/ianeff/thump/internal/tlsx",
		"github.com/ianeff/thump/internal/sealbox",
		"google.golang.org/grpc/credentials",
		"go.opentelemetry.io/contrib/exporters/autoexport",
		"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc",
		"go.opentelemetry.io/otel/sdk/metric",
		"go.opentelemetry.io/otel/sdk/metric/metricdata",
		"google.golang.org/grpc/codes",
		"google.golang.org/grpc/status",
		"sigs.k8s.io/yaml",
	)
}
