package trim_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/trim"
)

func TestProjection_ApplyThenGetReturnsTheFoldedIncident(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	p := trim.NewProjection()
	if err := p.Apply(signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: t0}); err != nil {
		t.Fatal(err)
	}

	got, ok := p.Get("fp-1")
	if !ok {
		t.Fatal("want an incident for fp-1, got none")
	}
	want := trim.Incident{Fingerprint: "fp-1", Stage: trim.StageDetected, Service: "checkout-api", UpdatedAt: t0}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong incident after Apply", diff)
	}
}

func TestProjection_GetReturnsFalseForAnUnseenFingerprint(t *testing.T) {
	t.Parallel()
	p := trim.NewProjection()

	_, ok := p.Get("never-applied")
	if ok {
		t.Error("want ok=false for a fingerprint the projection has never seen, got true")
	}
}

func TestProjection_ApplyFoldsOntoTheExistingIncidentForThatFingerprint(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Minute)

	p := trim.NewProjection()
	if err := p.Apply(signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(proposal.Set{SignalRef: "fp-1", SAOSnapshot: &proposal.SAO{Version: 1, AssembledAt: t1}}); err != nil {
		t.Fatal(err)
	}

	got, ok := p.Get("fp-1")
	if !ok {
		t.Fatal("want an incident for fp-1, got none")
	}
	// Service survives from the first Apply even though the second object
	// (proposal.Set) never carries it — proof Apply looks up the existing
	// incident and folds onto it, rather than overwriting with a fresh one.
	want := trim.Incident{Fingerprint: "fp-1", Stage: trim.StageProposed, Service: "checkout-api", UpdatedAt: t1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong incident after a second Apply", diff)
	}
}

func TestProjection_ApplyReturnsAnErrorWhenTheObjectCarriesNoFingerprint(t *testing.T) {
	t.Parallel()
	p := trim.NewProjection()

	// A signal.Detection is a type Apply recognizes — but this one's
	// Fingerprint is left as the zero value, so there's no key to store it
	// under.
	err := p.Apply(signal.Detection{OriginService: "checkout-api", DetectedAt: time.Now()})
	if !errors.Is(err, trim.ErrNoFingerprint) {
		t.Errorf("want ErrNoFingerprint for a Detection with no Fingerprint, got %v", err)
	}
}

func TestProjection_ApplyReturnsAnErrorForAnObjectTypeItDoesNotRecognize(t *testing.T) {
	t.Parallel()
	p := trim.NewProjection()

	err := p.Apply("not a boundary object")
	if !errors.Is(err, trim.ErrNoFingerprint) {
		t.Errorf("want ErrNoFingerprint for an unrecognized type, got %v", err)
	}
}

func TestProjection_SnapshotReturnsOneIncidentPerFingerprint(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	p := trim.NewProjection()
	if err := p.Apply(signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(signal.Detection{Fingerprint: "fp-2", OriginService: "billing-api", DetectedAt: t0}); err != nil {
		t.Fatal(err)
	}

	want := []trim.Incident{
		{Fingerprint: "fp-1", Stage: trim.StageDetected, Service: "checkout-api", UpdatedAt: t0},
		{Fingerprint: "fp-2", Stage: trim.StageDetected, Service: "billing-api", UpdatedAt: t0},
	}
	got := p.Snapshot()
	// Snapshot's order isn't part of the contract — it's reading a map — so
	// sort both sides by Fingerprint before comparing.
	sortByFingerprint := cmpopts.SortSlices(func(a, b trim.Incident) bool { return a.Fingerprint < b.Fingerprint })
	if diff := cmp.Diff(want, got, sortByFingerprint); diff != "" {
		t.Error("wrong snapshot contents", diff)
	}
}

func TestProjection_ConcurrentApplyIsRaceClean(t *testing.T) {
	t.Parallel()
	p := trim.NewProjection()

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		fp := fmt.Sprintf("fp-%d", i)
		wg.Go(func() {
			// t.Fatal is only safe to call from the goroutine actually
			// running the test function — calling it from a spawned
			// goroutine like this one doesn't stop the test the way you'd
			// expect. t.Errorf is documented safe from any goroutine, so
			// that's the one to reach for here.
			if err := p.Apply(signal.Detection{Fingerprint: fp, OriginService: "checkout-api", DetectedAt: time.Now()}); err != nil {
				t.Errorf("unexpected error applying %s: %v", fp, err)
			}
		})
	}
	wg.Wait()

	got := p.Snapshot()
	if len(got) != n {
		t.Errorf("want %d incidents after %d concurrent applies to distinct fingerprints, got %d", n, n, len(got))
	}
}
