package rca

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/evidence"
)

// loadDetection reads one signal.Detection fixture from clank's own
// testdata — the eight fixtures this suite grades live there, not under
// internal/rca.
func loadDetection(name string) (signal.Detection, error) {
	path := detectionPath(name)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: fixed testdata path, not user input
	if err != nil {
		return signal.Detection{}, fmt.Errorf("read detection %s: %w", name, err)
	}

	var d signal.Detection
	if err := yaml.Unmarshal(raw, &d); err != nil {
		return signal.Detection{}, fmt.Errorf("decode detection %s: %w", name, err)
	}
	return d, nil
}

// promQLByName inverts the evidence config: the fake Prometheus is keyed
// by the PromQL string the tool will actually send.
func promQLByName(queries map[string]evidence.Query) map[string]string {
	out := make(map[string]string, len(queries))
	for name, q := range queries {
		out[name] = q.Query
	}
	return out
}

func fakePrometheus(promQL map[string]string, values map[string]string) *httptest.Server {
	byExpr := make(map[string]string, len(values))
	for name, v := range values {
		if expr, ok := promQL[name]; ok {
			byExpr[expr] = v
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := byExpr[r.URL.Query().Get("query")]
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		resp := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []any{map[string]any{
					"metric": map[string]string{},
					"value":  []any{0, v},
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}
