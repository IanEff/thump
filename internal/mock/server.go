// Package mock provides in-process telemetry stub servers and embedded message
// brokers for zero-infrastructure simulation and testing — lightweight stubs
// execute locally with negligible resource overhead.
package mock

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	natssrv "github.com/nats-io/nats-server/v2/server"
)

// DefaultPrometheusFixture carries the default Prometheus instant and range
// query response — golden scenario telemetry from prom-cart-failure.json.
var DefaultPrometheusFixture = []byte(`{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {
        "metric": {
          "app": "cart",
          "job": "otel-demo"
        },
        "value": [
          1750000000,
          "1.25"
        ]
      }
    ]
  }
}`)

// DefaultLokiFixture carries the default Loki log query response — golden
// scenario telemetry from loki-cart-failure.json.
var DefaultLokiFixture = []byte(`{
  "status": "success",
  "data": {
    "resultType": "streams",
    "result": [
      {
        "stream": {
          "app": "cartservice",
          "namespace": "otel-demo"
        },
        "values": [
          [
            "1750000000000000000",
            "rpc error: code = Unavailable desc = connection error"
          ]
        ]
      }
    ]
  }
}`)

// TelemetryServer serves canned Prometheus and Loki HTTP telemetry endpoints —
// queries return configured fixtures or golden cart failure scenario defaults.
type TelemetryServer struct {
	srv           *http.Server
	listener      net.Listener
	url           string
	promFixture   []byte
	lokiFixture   []byte
	promResponses map[string][]byte
	lokiResponses map[string][]byte
	port          int
}

// TelemetryOption configures a TelemetryServer during initialization.
type TelemetryOption func(*TelemetryServer)

// WithPrometheusFixture overrides the default Prometheus JSON fixture served
// for instant and range queries.
func WithPrometheusFixture(fixture []byte) TelemetryOption {
	return func(s *TelemetryServer) {
		s.promFixture = fixture
	}
}

// WithLokiFixture overrides the default Loki JSON fixture served for range
// queries.
func WithLokiFixture(fixture []byte) TelemetryOption {
	return func(s *TelemetryServer) {
		s.lokiFixture = fixture
	}
}

// WithPrometheusResponses configures query-specific responses for Prometheus
// PromQL queries — unmatched queries fall back to the default fixture.
func WithPrometheusResponses(responses map[string][]byte) TelemetryOption {
	return func(s *TelemetryServer) {
		s.promResponses = responses
	}
}

// WithLokiResponses configures query-specific responses for Loki LogQL
// queries — unmatched queries fall back to the default fixture.
func WithLokiResponses(responses map[string][]byte) TelemetryOption {
	return func(s *TelemetryServer) {
		s.lokiResponses = responses
	}
}

// WithPort sets the TCP port for TelemetryServer — 0 selects a random
// ephemeral port.
func WithPort(port int) TelemetryOption {
	return func(s *TelemetryServer) {
		s.port = port
	}
}

// WithListener sets the net.Listener for TelemetryServer — overrides port
// binding configuration.
func WithListener(l net.Listener) TelemetryOption {
	return func(s *TelemetryServer) {
		s.listener = l
	}
}

// NewTelemetryServer initializes and starts an HTTP telemetry stub server —
// callers must invoke Close when finished to release listener resources.
func NewTelemetryServer(opts ...TelemetryOption) (*TelemetryServer, error) {
	s := &TelemetryServer{
		promFixture: DefaultPrometheusFixture,
		lokiFixture: DefaultLokiFixture,
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	handleProm := func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if query == "" && r.Method == http.MethodPost {
			_ = r.ParseForm()
			query = r.Form.Get("query")
		}
		w.Header().Set("Content-Type", "application/json")
		if resp, ok := s.promResponses[query]; ok {
			_, _ = w.Write(resp)
			return
		}
		_, _ = w.Write(s.promFixture)
	}
	mux.HandleFunc("/api/v1/query", handleProm)
	mux.HandleFunc("/api/v1/query_range", handleProm)

	handleLoki := func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if query == "" && r.Method == http.MethodPost {
			_ = r.ParseForm()
			query = r.Form.Get("query")
		}
		w.Header().Set("Content-Type", "application/json")
		if resp, ok := s.lokiResponses[query]; ok {
			_, _ = w.Write(resp)
			return
		}
		_, _ = w.Write(s.lokiFixture)
	}
	mux.HandleFunc("/loki/api/v1/query_range", handleLoki)

	if s.listener == nil {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
		if err != nil {
			return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", s.port, err)
		}
		s.listener = l
	}

	s.url = fmt.Sprintf("http://%s", s.listener.Addr().String())
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		_ = s.srv.Serve(s.listener)
	}()

	return s, nil
}

// URL returns the base HTTP URL of the running TelemetryServer.
func (s *TelemetryServer) URL() string {
	return s.url
}

// Addr returns the net.Addr of the underlying network listener.
func (s *TelemetryServer) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Close stops the HTTP server and releases its network listener.
func (s *TelemetryServer) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

// EmbeddedBroker runs an in-process NATS JetStream server for isolated tests
// and simulations — no external daemon or container is required.
type EmbeddedBroker struct {
	srv         *natssrv.Server
	storeDir    string
	isTempStore bool
}

type brokerConfig struct {
	host     string
	port     int
	storeDir string
}

// BrokerOption configures an EmbeddedBroker during initialization.
type BrokerOption func(*brokerConfig)

// WithBrokerHost sets the listening host for EmbeddedBroker — defaults to
// 127.0.0.1.
func WithBrokerHost(host string) BrokerOption {
	return func(c *brokerConfig) {
		c.host = host
	}
}

// WithBrokerPort sets the listening port for EmbeddedBroker — defaults to
// -1 for an ephemeral port.
func WithBrokerPort(port int) BrokerOption {
	return func(c *brokerConfig) {
		c.port = port
	}
}

// WithBrokerStoreDir sets the JetStream storage directory — defaults to a
// temporary directory cleaned up on Close.
func WithBrokerStoreDir(dir string) BrokerOption {
	return func(c *brokerConfig) {
		c.storeDir = dir
	}
}

// NewEmbeddedBroker starts an in-process NATS JetStream server — callers must
// invoke Close when finished to terminate the server.
func NewEmbeddedBroker(opts ...BrokerOption) (*EmbeddedBroker, error) {
	cfg := brokerConfig{
		host: "127.0.0.1",
		port: -1,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	storeDir := cfg.storeDir
	isTemp := false
	if storeDir == "" {
		tmp, err := os.MkdirTemp("", "thump-mock-broker-*")
		if err != nil {
			return nil, fmt.Errorf("create temporary jetstream store dir: %w", err)
		}
		storeDir = tmp
		isTemp = true
	}

	srv, err := natssrv.NewServer(&natssrv.Options{
		Host:      cfg.host,
		Port:      cfg.port,
		JetStream: true,
		StoreDir:  storeDir,
	})
	if err != nil {
		if isTemp {
			_ = os.RemoveAll(storeDir)
		}
		return nil, fmt.Errorf("create embedded nats server: %w", err)
	}

	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		if isTemp {
			_ = os.RemoveAll(storeDir)
		}
		return nil, errors.New("embedded nats server not ready for connections")
	}

	return &EmbeddedBroker{
		srv:         srv,
		storeDir:    storeDir,
		isTempStore: isTemp,
	}, nil
}

// ClientURL returns the NATS client connection URL.
func (b *EmbeddedBroker) ClientURL() string {
	if b.srv == nil {
		return ""
	}
	return b.srv.ClientURL()
}

// Ready reports whether the broker is running and ready for client
// connections.
func (b *EmbeddedBroker) Ready() bool {
	return b.srv != nil && b.srv.ReadyForConnections(100*time.Millisecond)
}

// Close terminates the embedded NATS server and removes any temporary store
// directory.
func (b *EmbeddedBroker) Close() error {
	if b.srv != nil {
		b.srv.Shutdown()
		b.srv.WaitForShutdown()
		b.srv = nil
	}
	if b.isTempStore && b.storeDir != "" {
		_ = os.RemoveAll(b.storeDir)
		b.storeDir = ""
	}
	return nil
}
