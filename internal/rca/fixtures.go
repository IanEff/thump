package rca

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/subjects"
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

// fakeLoki answers query_range with one canned log line per scripted LogQL
//
//	selector, and an empty result for anything else.
func fakeLoki(lines map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		line, ok := lines[r.URL.Query().Get("query")]
		if !ok {
			_, _ = w.Write([]byte(`{"data":{"result":[]}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"result": []any{map[string]any{
				"stream": map[string]string{},
				"values": [][2]string{{"0", line}},
			}}},
		})
	}))
}

// lokiLines resolves each entry in scripted whose key names a loki-plane
// subject to the LogQL buildLogQL will send for it, via idx's rule for that
// subject — the loki analogue of promQLByName+fakePrometheus's byExpr
// lookup, against the same map. A key naming no subject (an ordinary
// metrics query name) is skipped, which is what lets fakePrometheus and
// fakeLoki share Case.Evidence without a merge step.
func lokiLines(idx subjects.SubjectIndex, scripted map[string]string) map[string]string {
	byLogQL := make(map[string]string, len(scripted))
	for _, rule := range idx {
		if len(rule.Labels) == 0 || (rule.Plane != "" && rule.Plane != "loki") {
			continue
		}
		if line, ok := scripted[rule.Subject]; ok {
			byLogQL[logQLFor(rule.Namespace, rule.Labels)] = line
		}
	}
	return byLogQL
}

// logQLFor mirrors evidence.loki_tool.go's unexported buildLogQL(namespace,
// labels, "") — same sorted-matcher, quoted-value shape — so the fixture's
// map key matches exactly what LokiTool sends. Duplicated rather than
// exported: it's fixture code in a different package, not a reason for
// LokiTool to grow a public surface.
func logQLFor(namespace string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	matchers := make([]string, 0, len(keys)+1)
	matchers = append(matchers, "namespace="+strconv.Quote(namespace))
	for _, k := range keys {
		matchers = append(matchers, k+"="+strconv.Quote(labels[k]))
	}
	return "{" + strings.Join(matchers, ", ") + "}"
}
