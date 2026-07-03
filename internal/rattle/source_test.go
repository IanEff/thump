package rattle_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
)

func TestPromSource_ParsesABurnSeriesFromPrometheus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(cannedPromRangeResponse))
	}))
	defer srv.Close()

	got, err := rattle.NewPromSource(srv.URL).BurnSamples(context.Background(), someSLO())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(wantSamples(), got); diff != "" {
		t.Error("parsed the wrong burn series from the canned Prometheus body", diff)
	}
}

const cannedPromRangeResponse = `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {
        "metric": {"__name__": "slo:current_burn_rate:ratio", "sloth_id": "ceph-rgw-availability"},
        "values": [[1000, "1"], [1060, "2"], [1120, "4"], [1180, "8"]]
      }
    ]
  }
}`

func someSLO() rattle.SLO {
	return rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999}
}

func wantSamples() []rattle.Sample {
	base := time.Unix(1000, 0).UTC()
	return []rattle.Sample{
		{T: base, BurnRate: 1},
		{T: base.Add(60 * time.Second), BurnRate: 2},
		{T: base.Add(120 * time.Second), BurnRate: 4},
		{T: base.Add(180 * time.Second), BurnRate: 8},
	}
}
