package thump

import (
	"testing"

	"github.com/ianeff/thump/internal/config"
)

func TestBuildExecutor_LiveModeRequiresCluster(t *testing.T) {
	t.Parallel()
	// Off-cluster (CI, local), live mode can't build an in-cluster client.
	// It must error, not silently fall back to dry — a live executor that
	// can't reach the apiserver should refuse to start, not turn every action
	// into a runtime failure.
	_, _, err := buildExecutor(config.Thump{Executor: "live", KillSwitchPath: "/tmp/ks"})
	if err == nil {
		t.Fatal("live mode off-cluster must error, not silently degrade to dry")
	}
}

func TestBuildExecutor_DefaultsToDry(t *testing.T) {
	t.Parallel()
	exec, sw, err := buildExecutor(config.Thump{}) // no THUMP_EXECUTOR set
	if err != nil {
		t.Fatalf("dry mode must not error: %v", err)
	}
	if _, ok := exec.(DryRun); !ok {
		t.Fatalf("empty config must stay dry, got %T", exec)
	}
	if sw != nil {
		t.Fatal("dry mode needs no switch")
	}
}
