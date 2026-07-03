package rattle_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
)

func TestMain_PrintsVersionAndReturnsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := rattle.Main([]string{"-version"}, &out, &errb, "1.2.3", "abc123", "2026-07-01")
	if code != 0 {
		t.Errorf("version should exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "rattle 1.2.3") {
		t.Error("version output mmissing the version", cmp.Diff("rattle 1.2.3", out.String()))
	}
}

func TestMain_MissingPromURLReturnsOne(t *testing.T) {
	t.Setenv("PROM_URL", "") // hermetic — don't inherit the shell's
	var out, errb bytes.Buffer
	code := rattle.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing PROM_URL should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "PROM_URL") {
		t.Error("stderr should name the missing var", errb.String())
	}
}

func TestLoadSLOs_DeclaresTheLabContract(t *testing.T) {
	want := []rattle.SLO{
		{
			ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-rgw-availability:v1",
			Dependencies: []rattle.Dependency{{Name: "cephobjectstore", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "ceph-osd-latency", Object: "ceph-osd", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "ceph-osd-latency:v1",
			Dependencies: []rattle.Dependency{{Name: "cephblockpool", Role: "blocking"}, {Name: "ceph-node-1", Role: "blocking"}, {Name: "ceph-node-2", Role: "blocking"}, {Name: "ceph-node-3", Role: "blocking"}},
		},
		{
			ID: "ceph-health", Object: "ceph-cluster", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-health:v1",
			Dependencies: []rattle.Dependency{{Name: "cephcluster", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "argocd-sync", Object: "argocd", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "argocd-sync:v1",
			Dependencies: []rattle.Dependency{{Name: "cilium", Role: "blocking"}, {Name: "rook-operator", Role: "optional"}},
		},
	}
	if diff := cmp.Diff(want, rattle.LoadSLOsForTest()); diff != "" {
		t.Errorf("watch list drifted from the lab contract (-want +got):\n%s", diff)
	}
}

func TestLoadSLOs_EverySLODeclaresDependencies(t *testing.T) {
	for _, slo := range rattle.LoadSLOsForTest() {
		if len(slo.Dependencies) == 0 {
			t.Errorf("%s declares no dependencies — EnrichTopology will silently no-op for it", slo.ID)
		}
	}
}
