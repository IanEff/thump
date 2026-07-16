package thump

import (
	"testing"

	"github.com/ianeff/thump/internal/config"
)

func TestBuildExecutor_LiveModeWrapsGatedLive(t *testing.T) {
	t.Parallel()
	exec, sw := buildExecutor(config.Thump{Executor: "live", KillSwitchPath: "/tmp/ks"})
	if _, ok := exec.(GatedExecutor); !ok {
		t.Fatalf("live mode must wire a GatedExecutor, got %T", exec)
	}
	if sw == nil {
		t.Fatal("live mode must return a switch to reload")
	}
}

func TestBuildExecutor_DefaultsToDry(t *testing.T) {
	t.Parallel()
	exec, sw := buildExecutor(config.Thump{}) // no THUMP_EXECUTOR set
	if _, ok := exec.(DryRun); !ok {
		t.Fatalf("empty config must stay dry, got %T", exec)
	}
	if sw != nil {
		t.Fatal("dry mode needs no switch")
	}
}
