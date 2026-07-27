package beat_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/tlsxtest"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// fakeCollector is a minimal OTLP/gRPC trace receiver — just enough of
// TraceServiceServer to count the spans it was handed, standing in for
// Tempo so newOTLPExporter's https:// branch can be dialed without a
// cluster.
type fakeCollector struct {
	coltracepb.UnimplementedTraceServiceServer

	mu    sync.Mutex
	spans int
}

func (f *fakeCollector) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			f.spans += len(ss.GetSpans())
		}
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

func (f *fakeCollector) spanCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spans
}

// startFakeCollector listens on loopback under serverCfg and stops itself on
// test cleanup.
func startFakeCollector(t *testing.T, serverCfg tlsx.Config) (addr string, collector *fakeCollector) {
	t.Helper()

	tc, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	collector = &fakeCollector{}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tc)))
	coltracepb.RegisterTraceServiceServer(srv, collector)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return lis.Addr().String(), collector
}

// oneReadOnlySpan builds a single finished span through its own throwaway
// pipeline, independent of the exporter under test, so ExportSpans can be
// called directly against newOTLPExporter's output rather than through a
// second TracerProvider layer that would hide its return error.
func oneReadOnlySpan(t *testing.T) []sdktrace.ReadOnlySpan {
	t.Helper()

	mem := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(mem))
	_, span := tp.Tracer("test").Start(context.Background(), "probe")
	span.End()
	// InMemoryExporter.Shutdown clears its buffer (it's meant for reuse
	// across a suite), so the read has to happen before the shutdown that
	// would otherwise wipe it.
	spans := mem.GetSpans().Snapshots()
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatalf("shut down fixture provider: %v", err)
	}
	return spans
}

// TestNewOTLPExporter_ExportsSpansOverTLSAgainstATrustedCA pins R7b: an
// https:// endpoint must actually reach the collector, not just avoid an
// error at construction. Before this test, newOTLPExporter's tlsx.Client
// branch (trace.go) had never been dialed — otlpDialOptions' table only
// pins which branch gets chosen, never that the chosen branch delivers.
func TestNewOTLPExporter_ExportsSpansOverTLSAgainstATrustedCA(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	serverCfg := ca.Leaf(t, "collector", tlsxtest.IPSAN(net.ParseIP("127.0.0.1")))
	clientCfg := ca.Leaf(t, "beat")

	addr, collector := startFakeCollector(t, serverCfg)

	exp, err := beat.NewOTLPExporterForTest(context.Background(), "https://"+addr, clientCfg)
	if err != nil {
		t.Fatalf("NewOTLPExporterForTest: %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	if err := exp.ExportSpans(context.Background(), oneReadOnlySpan(t)); err != nil {
		t.Fatalf("ExportSpans over a trusted-CA TLS connection: %v", err)
	}
	if got := collector.spanCount(); got != 1 {
		t.Errorf("collector received %d spans, want 1 — the https:// branch must actually deliver, not just build without error", got)
	}
}

// TestNewOTLPExporter_RefusesACollectorCertificateFromAnUntrustedCA pins the
// other half of "verify properly": tlsx.Client must reject a collector
// whose certificate chains to a CA the beat wasn't given, not merely accept
// whatever it's handed. The collector's ClientCAs pool is deliberately set
// to the beat's own CA so that half of the handshake succeeds — isolating
// the one claim this test makes to the beat's RootCAs check.
func TestNewOTLPExporter_RefusesACollectorCertificateFromAnUntrustedCA(t *testing.T) {
	t.Parallel()

	homeCA := tlsxtest.NewCA(t)
	impostorCA := tlsxtest.NewCA(t)

	beatLeaf := homeCA.Leaf(t, "beat")
	collectorLeaf := impostorCA.Leaf(t, "collector", tlsxtest.IPSAN(net.ParseIP("127.0.0.1")))
	serverCfg := tlsx.Config{
		CertFile: collectorLeaf.CertFile,
		KeyFile:  collectorLeaf.KeyFile,
		CAFile:   beatLeaf.CAFile, // verify the beat's client cert against its own CA
	}

	addr, collector := startFakeCollector(t, serverCfg)

	exp, err := beat.NewOTLPExporterForTest(context.Background(), "https://"+addr, beatLeaf)
	if err != nil {
		t.Fatalf("NewOTLPExporterForTest: %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	// A cert-trust failure surfaces to the exporter as a retryable gRPC
	// Unavailable, so it backs off 5s before trying again rather than
	// failing fast (internal/retry.DefaultConfig.InitialInterval) — a
	// deadline below that backoff exits via ctx.Done() instead of waiting
	// it out.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := exp.ExportSpans(ctx, oneReadOnlySpan(t)); err == nil {
		t.Error("ExportSpans against a collector certificate from an untrusted CA succeeded, want a TLS verification error")
	}
	if got := collector.spanCount(); got != 0 {
		t.Errorf("collector received %d spans despite an untrusted certificate, want 0", got)
	}
}
