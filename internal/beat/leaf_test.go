package beat_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestBeatImportsNoBeat pins the kit's load-bearing invariant: internal/beat
// may import stdlib, the shared transport infrastructure (broker, publish,
// and the jetstream types they surface), tlsx (trace.go's Tracer and
// metrics.go's Metrics both build a *tls.Config from it rather than
// constructing one inline), the OTel tracing SDK plus grpc/credentials
// (trace.go's Tracer, which every beat's Main calls to build its span
// provider — credentials.NewTLS is the adapter from tlsx's *tls.Config to
// the credentials.TransportCredentials otlptracegrpc.WithTLSCredentials
// requires — and stage.go's Stage, which every beat's loop stages run
// through), the OTel metrics SDK plus autoexport and grpc/codes+status
// (otelmetrics.go's breakerExporter, which registers as the
// OTEL_METRICS_EXPORTER=otlp-breaker factory autoexport's compile-time
// instrumentation layer selects, and classifies gRPC failures from the
// wrapped otlpmetricgrpc exporter to decide whether to trip), the Prometheus
// client (metrics.go's Metrics, stage.go's StageRecorder), the AWS SDK plus
// its underlying smithy-go transport (objectstore.go's NewS3SegmentSink,
// which builds the S3 client a WAL ships sealed segments through, and the
// finalize middleware it installs to work around a GCS signing quirk), and
// sealbox (objectstore.go's EncryptingSink, which seals a segment before
// NewS3SegmentSink's inner sink ever sees it) — but NEVER a beat package.
// A clank, rattle, hiss, or thump import appearing here means the runtime
// kit has become a place where the planes mash together; this test is that
// regression's tripwire. Widen the allowlist below when tracing, metrics,
// or the object store grows a new dependency; never widen it with a beat
// import.
func TestBeatImportsNoBeat(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		`"github.com/ianeff/thump/internal/broker"`:                           true,
		`"github.com/ianeff/thump/internal/publish"`:                          true,
		`"github.com/nats-io/nats.go/jetstream"`:                              true,
		`"github.com/prometheus/client_golang/prometheus"`:                    true,
		`"github.com/prometheus/client_golang/prometheus/promhttp"`:           true,
		`"go.opentelemetry.io/otel"`:                                          true,
		`"go.opentelemetry.io/otel/codes"`:                                    true,
		`"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"`:   true,
		`"go.opentelemetry.io/otel/sdk/resource"`:                             true,
		`"go.opentelemetry.io/otel/sdk/trace"`:                                true,
		`"go.opentelemetry.io/otel/semconv/v1.26.0"`:                          true,
		`"go.opentelemetry.io/otel/trace"`:                                    true,
		`"go.opentelemetry.io/otel/trace/noop"`:                               true,
		`"github.com/aws/aws-sdk-go-v2/aws"`:                                  true,
		`"github.com/aws/aws-sdk-go-v2/config"`:                               true,
		`"github.com/aws/aws-sdk-go-v2/credentials"`:                          true,
		`"github.com/aws/aws-sdk-go-v2/service/s3"`:                           true,
		`"github.com/aws/smithy-go/middleware"`:                               true,
		`"github.com/aws/smithy-go/transport/http"`:                           true,
		`"github.com/ianeff/thump/internal/tlsx"`:                             true,
		`"github.com/ianeff/thump/internal/sealbox"`:                          true,
		`"google.golang.org/grpc/credentials"`:                                true,
		`"go.opentelemetry.io/contrib/exporters/autoexport"`:                  true,
		`"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"`: true,
		`"go.opentelemetry.io/otel/sdk/metric"`:                               true,
		`"go.opentelemetry.io/otel/sdk/metric/metricdata"`:                    true,
		`"google.golang.org/grpc/codes"`:                                      true,
		`"google.golang.org/grpc/status"`:                                     true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path := imp.Path.Value
			if isStdlib(path) || allowed[path] {
				continue
			}
			t.Errorf("%s imports %s — internal/beat must stay transport-only (stdlib + broker/publish/jetstream)",
				name, path)
		}
	}
}

// isStdlib reports whether a quoted import path is a standard-library package:
// stdlib paths have no dot in their first segment (no domain), where third-party
// paths look like "github.com/...".
func isStdlib(quoted string) bool {
	p := strings.Trim(quoted, `"`)
	first, _, _ := strings.Cut(p, "/")
	return !strings.Contains(first, ".")
}
