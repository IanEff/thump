package rattle_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/rattle"
)

func TestSignalFor_StampsTheKindObjectFingerprintAndSeamFields(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999}

	got := rattle.SignalFor(slo, "burn_rate_acceleration", 4.5, "accelerating", time.Unix(1000, 0), nil)

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

	got := rattle.SignalFor(slo, "burn_rate_acceleration", 4.5, "accelerating", time.Unix(1000, 0), nil)

	if got.Divergence.Observed != 4.5 {
		t.Error("SignalFor must quote the accel value it was GIVEN, not re-derive its own",
			cmp.Diff(4.5, got.Divergence.Observed))
	}
}

func TestSignalFor_StillStampsTheSameFingerprintThroughEnvelope(t *testing.T) {
	t.Parallel()
	var env rattle.Envelope = rattle.SLO{Object: "ceph-rgw", Tier: "tier-1", ContractRef: "ceph-rgw-availability:v1"}

	got := rattle.SignalFor(env, "burn_rate_acceleration", 2.0, "accelerating", time.Unix(1000, 0), nil)

	if got.Fingerprint != "slo_burn:ceph-rgw" {
		t.Error("fingerprint changed shape across the Envlope refactor", cmp.Diff("slo_burn:ceph-rgw", got.Fingerprint))
	}
}
