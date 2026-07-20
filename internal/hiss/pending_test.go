package hiss_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/hiss"
)

func TestPendingHolds_TakeReturnsTheGovernedRecordStored(t *testing.T) {
	t.Parallel()
	held := decision.Governed{
		Decision: decision.Decision{SignalRef: "fp-1", Verdict: decision.VerdictHold, EvaluatedAt: time.Now()},
	}
	h := hiss.NewPendingHolds()
	h.Record(held)

	got, ok := h.Take("fp-1")
	if !ok {
		t.Fatal("want Take to find the recorded fingerprint, got not-found")
	}
	if diff := cmp.Diff(held, got); diff != "" {
		t.Error("wrong Governed returned (-want +got)", diff)
	}
}

func TestPendingHolds_TakeIsInertForAnUnknownFingerprint(t *testing.T) {
	t.Parallel()
	h := hiss.NewPendingHolds()

	_, ok := h.Take("no-such-fp")
	if ok {
		t.Error("want Take to report not-found for an unrecorded fingerprint, got found")
	}
}

func TestPendingHolds_ASecondTakeForTheSameFingerprintIsInert(t *testing.T) {
	t.Parallel()
	h := hiss.NewPendingHolds()
	h.Record(decision.Governed{Decision: decision.Decision{SignalRef: "fp-1", Verdict: decision.VerdictHold}})

	if _, ok := h.Take("fp-1"); !ok {
		t.Fatal("first Take should find the recorded hold")
	}
	if _, ok := h.Take("fp-1"); ok {
		t.Error("want a second Take to find nothing (I-14: approving twice executes once), got found")
	}
}

func TestPendingHolds_ConcurrentRecordAndTakeIsRaceClean(t *testing.T) {
	t.Parallel()
	h := hiss.NewPendingHolds()
	var wg sync.WaitGroup
	for i := range 50 {
		fp := fmt.Sprintf("fp-%d", i)
		wg.Go(func() {
			h.Record(decision.Governed{Decision: decision.Decision{SignalRef: fp, Verdict: decision.VerdictHold}})
		})
		wg.Go(func() {
			h.Take(fp)
		})
	}
	wg.Wait()
}
