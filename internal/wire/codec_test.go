package wire_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/wire"
)

func TestCodec_DetectionRoundTrips(t *testing.T) {
	t.Parallel()
	in := signal.Detection{Name: "rgw-burn", Fingerprint: "slo_burn:ceph-rgw", ServiceTier: "tier-1"}

	data, err := wire.Marshal(in)
	if err != nil {
		t.Fatal("marshal:", err)
	}

	var out signal.Detection
	if err := wire.Unmarshal(data, &out); err != nil {
		t.Fatal("unmarshal:", err)
	}

	if diff := cmp.Diff(in, out); diff != "" {
		t.Error("detection didn't survive the codec (-want, +got)", diff)
	}
}
