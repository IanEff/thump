package rattle_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/rattle"
)

func TestBuildSources_WarnsOnEverySilentFallback(t *testing.T) {
	tests := map[string]struct {
		cfg      config.Rattle
		wantMsgs []string
	}{
		"buildSources warns for both topology and traffic when neither is configured": {
			cfg: config.Rattle{PromURL: "http://prom:9090"},
			wantMsgs: []string{
				"no topology source configured — reconciling without a blast-radius map",
				"no traffic source configured — reconciling without traffic enrichment",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// captureLog mutates the process-wide default logger — no t.Parallel().
			getLines := captureLog(t)

			topo, traffic, err := rattle.BuildSourcesForTest(tc.cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if topo != nil {
				t.Error("want a nil TopologySource when whir is unconfigured")
			}
			if traffic != nil {
				t.Error("want a nil TrafficSource when RATTLE_TRAFFIC is unconfigured")
			}

			got := warnMessages(getLines())
			for _, want := range tc.wantMsgs {
				if !slices.Contains(got, want) {
					t.Errorf("want a WARN %q, got %v", want, got)
				}
			}
		})
	}
}

func TestBuildSources_FullyConfiguredReachesRealTopologyAndTraffic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.yaml")
	statePath := filepath.Join(dir, "state-queries.yaml")
	trafficPath := filepath.Join(dir, "traffic-queries.yaml")
	for _, f := range []string{catalogPath, statePath, trafficPath} {
		if err := os.WriteFile(f, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Rattle{
		PromURL:          "http://prom:9090",
		WhirCatalog:      catalogPath,
		WhirStateQueries: statePath,
		Traffic:          trafficPath,
	}
	topo, traffic, err := rattle.BuildSourcesForTest(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := topo.(*rattle.WhirTopologySource); !ok {
		t.Errorf("fully-configured buildSources must reach a real WhirTopologySource, got %T", topo)
	}
	if _, ok := traffic.(*rattle.HubbleTrafficSource); !ok {
		t.Errorf("fully-configured buildSources must reach a real HubbleTrafficSource, got %T", traffic)
	}
}
