package rattle_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
)

func TestHubbleTrafficSource_TrafficSamples(t *testing.T) {
	stub := map[string]string{
		"a3":     `{"data":{"result":[{"value":[0,"3"]}]}}`,
		"t12":    `{"data":{"result":[{"value":[0,"12"]}]}}`,
		"a20":    `{"data":{"result":[{"value":[0,"20"]}]}}`,
		"t10":    `{"data":{"result":[{"value":[0,"10"]}]}}`,
		"a0":     `{"data":{"result":[{"value":[0,"0"]}]}}`,
		"tEmpty": `{"data":{"result":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(stub[r.URL.Query().Get("query")]))
	}))
	defer srv.Close()

	tests := []struct {
		name     string
		aff, tot string
		wantLen  int
		wantPct  float64
	}{
		{"computes affected share", "a3", "t12", 1, 0.25},
		{"clamps affected>total to 1.0", "a20", "t10", 1, 1.0},
		{"quiet when total has no series", "a0", "tEmpty", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &rattle.HubbleTrafficSource{
				BaseURL: srv.URL, Client: http.DefaultClient,
				Queries: map[string]rattle.TrafficQueries{"x": {Affected: tt.aff, Total: tt.tot}},
			}
			got, err := h.TrafficSamples(context.Background(), rattle.SLO{ID: "x"})
			if err != nil {
				t.Fatalf("TrafficSamples: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("want %d samples, got %d", tt.wantLen, len(got))
			}
			if tt.wantLen == 1 {
				if diff := cmp.Diff(tt.wantPct, got[0].AffectedPct); diff != "" {
					t.Error("affected share wrong (-want +got):\n" + diff)
				}
			}
		})
	}
}

func TestHubbleTrafficSource_PropagatesQueryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := &rattle.HubbleTrafficSource{
		BaseURL: srv.URL, Client: http.DefaultClient,
		Queries: map[string]rattle.TrafficQueries{"x": {Affected: "a", Total: "t"}},
	}
	if _, err := h.TrafficSamples(context.Background(), rattle.SLO{ID: "x"}); err == nil {
		t.Error("a failing Hubble query must return an error, not quiet absence")
	}
}
