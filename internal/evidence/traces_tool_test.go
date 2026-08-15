package evidence_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/subjects"
)

func TestTracesTool_Run(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input      string
		body       string
		statusCode int
		want       proposal.EvidenceRef
	}{
		"Run given matching traces returns live evidence": {
			input: `{"namespace":"otel-demo","labels":{"resource.service.name":"cart"},"errorsOnly":true}`,
			body:  `{"traces":[{"traceID":"abc","rootServiceName":"load-generator","rootTraceName":"user_add_to_cart"}]}`,
			want: proposal.EvidenceRef{
				Tool:    "traces",
				Query:   `{resource.service.name="cart" && status=error}`,
				Summary: "1 trace(s); last root: load-generator/user_add_to_cart",
				Ref:     "tempo://otel-demo/resource.service.name=cart",
				Live:    true,
				Subject: "cart",
			},
		},
		"Run given zero traces returns a non-live ref rather than an error": {
			input: `{"namespace":"otel-demo","labels":{"resource.service.name":"cart"}}`,
			body:  `{"traces":[]}`,
			want: proposal.EvidenceRef{
				Tool:    "traces",
				Query:   `{resource.service.name="cart"}`,
				Summary: "no matching traces",
				Ref:     "tempo://otel-demo/resource.service.name=cart",
				Live:    false,
				Subject: "cart",
			},
		},
		"Run given a non-200 returns a non-live ref quoting the status": {
			input:      `{"namespace":"otel-demo","labels":{"resource.service.name":"cart"}}`,
			statusCode: http.StatusInternalServerError,
			want: proposal.EvidenceRef{
				Tool:    "traces",
				Query:   `{resource.service.name="cart"}`,
				Summary: "tempo returned status: 500 Internal Server Error",
				Live:    false,
				Subject: "cart",
			},
		},
		"Run given an undecodable body returns a non-live ref": {
			input: `{"namespace":"otel-demo","labels":{"resource.service.name":"cart"}}`,
			body:  `invalid json`,
			want: proposal.EvidenceRef{
				Tool:    "traces",
				Query:   `{resource.service.name="cart"}`,
				Summary: "decode tempo response: invalid character 'i' looking for beginning of value",
				Live:    false,
				Subject: "cart",
			},
		},
		"Run given an invalid lookback returns a non-live ref": {
			input: `{"namespace":"otel-demo","labels":{"resource.service.name":"cart"},"lookback":"bad"}`,
			want: proposal.EvidenceRef{
				Tool:    "traces",
				Query:   `{resource.service.name="cart"}`,
				Summary: `invalid lookback: time: invalid duration "bad"`,
				Live:    false,
				Subject: "cart",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/search" {
					t.Errorf("wrong path: %s", r.URL.Path)
				}
				if tc.statusCode != 0 {
					w.WriteHeader(tc.statusCode)
					return
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer ts.Close()

			subIdx := subjects.SubjectIndex{
				{
					Subject: "cart",
					Coordinates: subjects.Coordinates{
						Namespace: "otel-demo",
						Labels:    map[string]string{"resource.service.name": "cart"},
					},
				},
			}
			tool := &evidence.TracesTool{BaseURL: ts.URL, Subjects: subIdx}
			got, err := tool.Run(t.Context(), json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong EvidenceRef (-want +got)\n", diff)
			}
		})
	}
}

func TestTracesTool_Run_RefusesAnAttributeKeyThatCouldEscapeTheQuery(t *testing.T) {
	t.Parallel()
	for _, key := range []string{`x"} && {true`, "svc name", "resource.service.name;drop", ""} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			var called bool
			ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				called = true
				if q := r.URL.Query().Get("q"); strings.Count(q, "{") != 1 {
					t.Errorf("model input reshaped the query: %s", q)
				}
			}))
			defer ts.Close()

			tool := &evidence.TracesTool{BaseURL: ts.URL}
			args := fmt.Sprintf(`{"namespace":"otel-demo","labels":{%q:"cart"}}`, key)
			got, err := tool.Run(t.Context(), json.RawMessage(args))
			if err != nil {
				t.Fatalf("a rejected key is backend reality, not a programmer fault: %v", err)
			}
			if got.Live {
				t.Error("a refused query must never be Live")
			}
			if called {
				t.Error("a refused query must not reach the backend at all")
			}
		})
	}
}

func TestTracesTool_Run_NeverPutsTheNamespaceInTheQuery(t *testing.T) {
	t.Parallel()
	var query string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		_, _ = io.WriteString(w, `{"traces":[]}`)
	}))
	defer ts.Close()

	tool := &evidence.TracesTool{BaseURL: ts.URL}
	_, err := tool.Run(t.Context(), json.RawMessage(`{"namespace":"otel-demo","labels":{"resource.service.name":"cart"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(query, "otel-demo") || strings.Contains(query, "namespace") {
		t.Errorf("query contains namespace information: %s", query)
	}
}

func TestTracesTool_SpecAdvertisesEveryAuthoredTraceSelector(t *testing.T) {
	t.Parallel()
	idx := subjects.SubjectIndex{
		{
			Subject: "cart",
			Plane:   "traces",
			Coordinates: subjects.Coordinates{
				Namespace: "otel-demo",
				Labels:    map[string]string{"resource.service.name": "cart"},
			},
		},
	}
	tool := &evidence.TracesTool{Subjects: idx}
	spec := tool.Spec()
	if spec.Name != "traces" {
		t.Errorf("wrong tool name: want 'traces', got %q", spec.Name)
	}
	wantSelector := "cart: namespace=otel-demo, resource.service.name=cart"
	if !strings.Contains(spec.Description, wantSelector) {
		t.Errorf("spec description missing authored selector %q: %s", wantSelector, spec.Description)
	}
}
