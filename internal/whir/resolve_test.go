package whir_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ianeff/thump/internal/whir"
)

func TestResolver_State_ThreeValueTruth(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query().Get("query")
		switch q {
		case "up_query":
			_, err := w.Write([]byte(`{"data":{"result":[{"value":[0,"1"]}]}}`))
			if err != nil {
				t.Fatalf("Write up_query: %v", err)
			}
		case "down_query":
			_, err := w.Write([]byte(`{"data":{"result":[{"value":[0,"0"]}]}}`))
			if err != nil {
				t.Fatalf("Write down_query: %v", err)
			}
		case "error_query":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	r := &whir.Resolver{
		BaseURL: srv.URL,
		Client:  http.DefaultClient,
		Queries: map[string]string{
			"up":   "up_query",
			"down": "down_query",
			"err":  "error_query",
			// "missing" deliberately absent from this map
		},
	}

	tests := []struct {
		name string
		dep  string
		want string
	}{
		{"result 1 -> healthy", "up", whir.StateHealthy},
		{"result 0 -> degraded", "down", whir.StateDegraded},
		{"HTTP 500 -> unknown", "err", whir.StateUnknown},
		{"unmapped dependency -> unknown, no HTTP call", "missing", whir.StateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callsBefore := calls
			got := r.State(context.Background(), tt.dep)
			if got != tt.want {
				t.Errorf("State(%q) = %q, want %q", tt.dep, got, tt.want)
			}
			if tt.dep == "missing" && calls != callsBefore {
				t.Errorf("State(%q) made an HTTP call, want none", tt.dep)
			}
		})
	}
}
