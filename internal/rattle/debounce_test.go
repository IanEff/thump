package rattle_test

import (
	"testing"
	"time"

	"github.com/ianeff/thump/internal/rattle"
)

func TestDebouncer_SuppressesARepeatWithinTheHoldWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	d := rattle.NewDebouncer(10 * time.Minute)

	if !d.Allow("slo_burn:ceph-rgw", now) {
		t.Fatal("first sighting of a fingerprint must be allowed")
	}
	if d.Allow("slo_burn:ceph-rgw", now.Add(5*time.Minute)) {
		t.Error("a repeat inside the hold window must be suppressed")
	}
	if !d.Allow("slo_burn:ceph-rgw", now.Add(11*time.Minute)) {
		t.Error("after the hold window elapses, the fingerprint may fire again")
	}
}
