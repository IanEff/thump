package rattle_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/ianeff/clank/internal/rattle"
	"github.com/ianeff/clank/internal/signal"
)

func TestSignalFor_StampsTheKindObjectFingerprintAndSeamFields(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999}

	got := rattle.SignalFor(slo, "burn_rate_acceleration", 4.5, time.Unix(1000, 0))

	want := signal.Detection{
		Fingerprint:   "slo_burn:ceph-rgw", // kind:object -- rattle's dedupe key
		OriginService: "ceph-rgw",
		ServiceTier:   "tier-1",
		DetectorType:  "burn_rate_acceleration",
		DetectedAt:    time.Unix(1000, 0),
	}

	ignore := cmpopts.IgnoreFields(signal.Detection{}, "Name", "Divergence", "Topology", "Traffic", "Impact", "ContractRef")
	if diff := cmp.Diff(want, got, ignore); diff != "" {
		t.Error("emitted Detection has the wrong seam fields", diff)
	}
}

func TestSignalFor_QuotesTheAccelerationInDivergence(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw", Tier: "tier-1"}

	got := rattle.SignalFor(slo, "burn_rate_acceleration", 4.5, time.Unix(1000, 0))

	if got.Divergence.Observed != 4.5 {
		t.Error("SignalFor must quote the accel value it was GIVEN, not re-derive its own",
			cmp.Diff(4.5, got.Divergence.Observed))
	}
}
