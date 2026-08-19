package mock_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/mock"
)

func TestFixtures_MatchDiskGoldenFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		diskPath string
		defaultB []byte
	}{
		"Prometheus default fixture matches disk fixture file": {
			diskPath: filepath.Join("..", "..", "test", "fixtures", "telemetry", "prom-cart-failure.json"),
			defaultB: mock.DefaultPrometheusFixture,
		},
		"Loki default fixture matches disk fixture file": {
			diskPath: filepath.Join("..", "..", "test", "fixtures", "telemetry", "loki-cart-failure.json"),
			defaultB: mock.DefaultLokiFixture,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diskBytes, err := os.ReadFile(tc.diskPath) //nolint:gosec // G304: fixed testdata path, not user input
			if err != nil {
				t.Fatalf("failed to read disk fixture %s: %v", tc.diskPath, err)
			}

			var want any
			if err := json.Unmarshal(diskBytes, &want); err != nil {
				t.Fatalf("failed to unmarshal disk fixture %s: %v", tc.diskPath, err)
			}

			var got any
			if err := json.Unmarshal(tc.defaultB, &got); err != nil {
				t.Fatalf("failed to unmarshal default fixture: %v", err)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("default fixture mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTelemetryServer_Defaults(t *testing.T) {
	t.Parallel()

	srv, err := mock.NewTelemetryServer()
	if err != nil {
		t.Fatalf("NewTelemetryServer returned unexpected error: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
	})

	if srv.URL() == "" {
		t.Fatal("srv.URL() is empty")
	}
	if srv.Addr() == nil {
		t.Fatal("srv.Addr() is nil")
	}

	client := &http.Client{Timeout: 5 * time.Second}

	tests := map[string]struct {
		path        string
		method      string
		wantStatus  int
		wantJSONObj any
	}{
		"Health check returns 200 OK with ready status": {
			path:        "/ready",
			method:      http.MethodGet,
			wantStatus:  http.StatusOK,
			wantJSONObj: map[string]any{"status": "ready"},
		},
		"Prometheus instant query returns default cart failure fixture": {
			path:        "/api/v1/query?query=up",
			method:      http.MethodGet,
			wantStatus:  http.StatusOK,
			wantJSONObj: unmarshalJSON(t, mock.DefaultPrometheusFixture),
		},
		"Prometheus range query returns default cart failure fixture": {
			path:        "/api/v1/query_range?query=rate(http_requests_total[5m])",
			method:      http.MethodGet,
			wantStatus:  http.StatusOK,
			wantJSONObj: unmarshalJSON(t, mock.DefaultPrometheusFixture),
		},
		"Loki range query returns default cart failure fixture": {
			path:        "/loki/api/v1/query_range?query={app=\"cart\"}",
			method:      http.MethodGet,
			wantStatus:  http.StatusOK,
			wantJSONObj: unmarshalJSON(t, mock.DefaultLokiFixture),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), tc.method, srv.URL()+tc.path, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					t.Errorf("error closing response body: %v", closeErr)
				}
			}()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status code mismatch: want %d, got %d", tc.wantStatus, resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}

			var gotJSON any
			if err := json.Unmarshal(body, &gotJSON); err != nil {
				t.Fatalf("failed to unmarshal body JSON: %v", err)
			}

			if diff := cmp.Diff(tc.wantJSONObj, gotJSON); diff != "" {
				t.Errorf("response body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTelemetryServer_CustomFixturesAndResponses(t *testing.T) {
	t.Parallel()

	customProm := []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"app":"payment"},"value":[100,"42.0"]}]}}`)
	customLoki := []byte(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"payment"},"values":[["100","payment failed"]]}]}}`)
	overrideProm := []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"app":"override"},"value":[200,"99.9"]}]}}`)
	overrideLoki := []byte(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"override"},"values":[["200","override log"]]}]}}`)

	srv, err := mock.NewTelemetryServer(
		mock.WithPrometheusFixture(customProm),
		mock.WithLokiFixture(customLoki),
		mock.WithPrometheusResponses(map[string][]byte{
			"custom_metric_query": overrideProm,
		}),
		mock.WithLokiResponses(map[string][]byte{
			`{app="custom"}`: overrideLoki,
		}),
	)
	if err != nil {
		t.Fatalf("NewTelemetryServer returned unexpected error: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
	})

	client := &http.Client{Timeout: 5 * time.Second}

	tests := map[string]struct {
		path        string
		wantJSONObj any
	}{
		"Prometheus instant query returns custom fixture when unmapped": {
			path:        "/api/v1/query?query=fallback_query",
			wantJSONObj: unmarshalJSON(t, customProm),
		},
		"Prometheus instant query returns mapped response when query matches exact expression": {
			path:        "/api/v1/query?query=custom_metric_query",
			wantJSONObj: unmarshalJSON(t, overrideProm),
		},
		"Prometheus range query returns mapped response when query matches exact expression": {
			path:        "/api/v1/query_range?query=custom_metric_query",
			wantJSONObj: unmarshalJSON(t, overrideProm),
		},
		"Loki query returns custom fixture when unmapped": {
			path:        "/loki/api/v1/query_range?query={app=\"other\"}",
			wantJSONObj: unmarshalJSON(t, customLoki),
		},
		"Loki query returns mapped response when query matches exact expression": {
			path:        "/loki/api/v1/query_range?query={app=\"custom\"}",
			wantJSONObj: unmarshalJSON(t, overrideLoki),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL()+tc.path, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					t.Errorf("error closing response body: %v", closeErr)
				}
			}()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read response body: %v", err)
			}

			var gotJSON any
			if err := json.Unmarshal(body, &gotJSON); err != nil {
				t.Fatalf("failed to unmarshal body JSON: %v", err)
			}

			if diff := cmp.Diff(tc.wantJSONObj, gotJSON); diff != "" {
				t.Errorf("response body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTelemetryServer_Close(t *testing.T) {
	t.Parallel()

	srv, err := mock.NewTelemetryServer()
	if err != nil {
		t.Fatalf("NewTelemetryServer returned unexpected error: %v", err)
	}

	serverURL := srv.URL()
	if err := srv.Close(); err != nil {
		t.Fatalf("srv.Close() returned error: %v", err)
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, serverURL+"/ready", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error connecting to closed server, got nil")
	}
}

func TestTelemetryServer_Integration(t *testing.T) {
	t.Parallel()

	srv, err := mock.NewTelemetryServer()
	if err != nil {
		t.Fatalf("NewTelemetryServer returned unexpected error: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
	})

	t.Run("httpx InstantQuery decodes vector result from mock telemetry server", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		result, err := httpx.InstantQuery(ctx, nil, srv.URL(), "cart_failure_rate")
		if err != nil {
			t.Fatalf("InstantQuery failed: %v", err)
		}

		if len(result.Data.Result) != 1 {
			t.Fatalf("expected 1 result, got %d", len(result.Data.Result))
		}

		metricApp := result.Data.Result[0].Metric["app"]
		if metricApp != "cart" {
			t.Errorf("metric app mismatch: want cart, got %s", metricApp)
		}

		var val string
		if err := json.Unmarshal(result.Data.Result[0].Value[1], &val); err != nil {
			t.Fatalf("failed to unmarshal metric value: %v", err)
		}
		if val != "1.25" {
			t.Errorf("metric value mismatch: want 1.25, got %s", val)
		}
	})

	t.Run("evidence LokiTool receives live logs from mock telemetry server", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		tool := &evidence.LokiTool{
			BaseURL: srv.URL(),
		}

		ref, err := tool.Run(ctx, json.RawMessage(`{"namespace":"otel-demo","labels":{"app":"cartservice"}}`))
		if err != nil {
			t.Fatalf("LokiTool.Run failed: %v", err)
		}

		if !ref.Live {
			t.Errorf("expected ref.Live to be true, got false (summary: %s)", ref.Summary)
		}
	})
}

func TestEmbeddedBroker_Lifecycle(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts []mock.BrokerOption
	}{
		"Embedded broker starts on ephemeral port and handles JetStream publish-subscribe": {
			opts: nil,
		},
		"Embedded broker starts on configured custom host": {
			opts: []mock.BrokerOption{
				mock.WithBrokerHost("127.0.0.1"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			broker, err := mock.NewEmbeddedBroker(tc.opts...)
			if err != nil {
				t.Fatalf("NewEmbeddedBroker returned unexpected error: %v", err)
			}

			if !broker.Ready() {
				t.Fatal("expected broker.Ready() to be true")
			}

			clientURL := broker.ClientURL()
			if clientURL == "" {
				t.Fatal("broker.ClientURL() is empty")
			}

			nc, err := nats.Connect(clientURL)
			if err != nil {
				t.Fatalf("failed to connect to embedded NATS broker: %v", err)
			}

			js, err := jetstream.New(nc)
			if err != nil {
				nc.Close()
				t.Fatalf("failed to create jetstream context: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			streamName := "TEST_STREAM"
			_, err = js.CreateStream(ctx, jetstream.StreamConfig{
				Name:     streamName,
				Subjects: []string{"test.subject"},
			})
			if err != nil {
				nc.Close()
				t.Fatalf("failed to create jetstream stream: %v", err)
			}

			pubAck, err := js.Publish(ctx, "test.subject", []byte("payload"))
			if err != nil {
				nc.Close()
				t.Fatalf("failed to publish to stream: %v", err)
			}
			if pubAck.Stream != streamName {
				t.Errorf("pubAck stream mismatch: want %s, got %s", streamName, pubAck.Stream)
			}

			nc.Close()

			if err := broker.Close(); err != nil {
				t.Fatalf("broker.Close() returned error: %v", err)
			}

			if broker.Ready() {
				t.Error("expected broker.Ready() to be false after Close")
			}

			// Connecting after Close must fail
			_, err = nats.Connect(clientURL, nats.Timeout(200*time.Millisecond))
			if err == nil {
				t.Error("expected nats.Connect to fail on closed broker, got nil error")
			}
		})
	}
}

func unmarshalJSON(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("failed to unmarshal json: %v", err)
	}
	return v
}
