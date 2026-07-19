package trim_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/trim"
)

func TestTick_FoldsEveryBoundaryObjectTypeIntoTheProjection(t *testing.T) {
	t.Parallel()
	const fp = "fp-1"
	const svc = "checkout-api"
	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	inbox := t.TempDir()
	set := proposal.Set{SignalRef: fp, SAOSnapshot: &proposal.SAO{Version: 1, AssembledAt: t0.Add(time.Minute)}}

	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: fp, OriginService: svc, DetectedAt: t0})
	writeYAML(t, filepath.Join(inbox, "proposals"), "prop-1.yaml", set)
	writeYAML(t, filepath.Join(inbox, "decisions"), "dec-1.yaml", decision.Governed{
		Decision: decision.Decision{
			SignalRef:     fp,
			Verdict:       decision.VerdictApproved,
			PolicyVersion: "policy-v3",
			EvaluatedAt:   t0.Add(2 * time.Minute),
		},
		Set: set,
	})
	writeYAML(t, filepath.Join(inbox, "outcomes"), "out-1.yaml", outcome.Outcome{
		SignalRef:   fp,
		DecisionRef: "dec-1",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultApplied,
		ExecutedAt:  t0.Add(3 * time.Minute),
	})

	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("golden run must not error:", err)
	}

	got, ok := tr.Proj.Get(fp)
	if !ok {
		t.Fatal("want an incident for fp-1, got none")
	}
	want := trim.Incident{Fingerprint: fp, Stage: trim.StageApplied, Service: svc, UpdatedAt: t0.Add(3 * time.Minute)}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong incident after Tick folded all four objects", diff)
	}

	for _, processed := range []string{
		filepath.Join(inbox, "detections", "processed", "det-1.yaml"),
		filepath.Join(inbox, "proposals", "processed", "prop-1.yaml"),
		filepath.Join(inbox, "decisions", "processed", "dec-1.yaml"),
		filepath.Join(inbox, "outcomes", "processed", "out-1.yaml"),
	} {
		if _, err := os.Stat(processed); err != nil {
			t.Errorf("want %s archived after a successful fold: %v", processed, err)
		}
	}
}

func TestTick_QuarantinesAFileThatFailsToDecodeAndSurvives(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	propDir := filepath.Join(inbox, "proposals")
	if err := os.MkdirAll(propDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(propDir, "poison.yaml"), []byte("proposals: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("one bad file must not fail the pass:", err)
	}

	if _, err := os.Stat(filepath.Join(propDir, "quarantine", "poison.yaml")); err != nil {
		t.Error("unparseable input must land in quarantine/, not vanish:", err)
	}
	if got := tr.Proj.Snapshot(); len(got) != 0 {
		t.Errorf("a poison file must not reach the projection, got %d incidents", len(got))
	}

	// the loop survives its own quarantine — a second pass over the
	// now-clean inbox is a no-op, not a repeat failure.
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own quarantine:", err)
	}
}

func TestTick_QuarantinesAFileWhoseDecodedObjectHasNoFingerprint(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	// Valid YAML, valid signal.Detection shape — Fingerprint just left
	// empty, so Projection.Apply has nowhere to fold it.
	writeYAML(t, filepath.Join(inbox, "detections"), "no-fp.yaml",
		signal.Detection{OriginService: "checkout-api", DetectedAt: time.Now()})

	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("a fingerprint-less file must not fail the whole pass:", err)
	}

	if _, err := os.Stat(filepath.Join(inbox, "detections", "quarantine", "no-fp.yaml")); err != nil {
		t.Error("an object Apply rejects must land in quarantine/, not vanish:", err)
	}
	if got := tr.Proj.Snapshot(); len(got) != 0 {
		t.Errorf("a rejected object must not reach the projection, got %d incidents", len(got))
	}
}

func TestTick_IsANoOpWhenTheInboxHasNoSubdirectoriesYet(t *testing.T) {
	t.Parallel()
	tr := &trim.Transport{Inbox: t.TempDir(), Proj: trim.NewProjection()}

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("a freshly-created, empty inbox must not error:", err)
	}
	if got := tr.Proj.Snapshot(); len(got) != 0 {
		t.Errorf("want no incidents from an empty inbox, got %d", len(got))
	}
}

func TestTick_ReturnsContextErrorWithoutTouchingTheInbox(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})
	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tr.Tick(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
	if got := tr.Proj.Snapshot(); len(got) != 0 {
		t.Errorf("a canceled Tick must not have processed anything, got %d incidents", len(got))
	}
}

// TestSnapshot_StillShowsAnIncidentAfterTickHasAlreadyArchivedItsFile pins
// the bug a one-shot `trim incidents` invocation would otherwise hit: Tick
// archives what it processes (correct for a long-running consumer that keeps
// its Projection alive across many polls), so a second, fresh process has no
// memory of what an earlier Tick already consumed. Snapshot has to find it
// anyway by also reading processed/, or every one-shot query after the first
// would report fewer incidents than actually exist.
func TestSnapshot_StillShowsAnIncidentAfterTickHasAlreadyArchivedItsFile(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})

	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("first Tick must not error:", err)
	}

	proj, err := tr.Snapshot(context.Background())
	if err != nil {
		t.Fatal("Snapshot must not error against an already-consumed inbox:", err)
	}

	got, ok := proj.Get("fp-1")
	if !ok {
		t.Fatal("want an incident for fp-1 even though its source file was already archived, got none")
	}
	if got.Stage != trim.StageDetected {
		t.Errorf("want Stage detected, got %v", got.Stage)
	}
}

// TestSnapshot_DoesNotMoveOrMutateAnyFileOnDisk pins that Snapshot is
// read-only — unlike Tick, it never archives or quarantines, so a one-shot
// query has no side effects on the inbox it reads.
func TestSnapshot_DoesNotMoveOrMutateAnyFileOnDisk(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	path := filepath.Join(inbox, "detections", "det-1.yaml")
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})

	tr := &trim.Transport{Inbox: inbox}
	if _, err := tr.Snapshot(context.Background()); err != nil {
		t.Fatal("Snapshot must not error:", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Error("want the source file to remain exactly where it was — Snapshot is read-only:", err)
	}
}

// TestSnapshot_SkipsAFileItCannotDecodeWithoutFailingThePass pins that a bad
// file degrades to "not counted," not "the whole query errors" — and, since
// Snapshot never quarantines, the bad file is left exactly where it was.
func TestSnapshot_SkipsAFileItCannotDecodeWithoutFailingThePass(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	propDir := filepath.Join(inbox, "proposals")
	if err := os.MkdirAll(propDir, 0o750); err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(propDir, "poison.yaml")
	if err := os.WriteFile(poison, []byte("proposals: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := &trim.Transport{Inbox: inbox}
	proj, err := tr.Snapshot(context.Background())
	if err != nil {
		t.Fatal("one bad file must not fail the whole snapshot:", err)
	}
	if got := proj.Snapshot(); len(got) != 0 {
		t.Errorf("a poison file must not reach the projection, got %d incidents", len(got))
	}
	if _, err := os.Stat(poison); err != nil {
		t.Error("Snapshot must not move or quarantine a bad file:", err)
	}
}

// TestSnapshot_ReturnsContextErrorWithoutReadingTheInbox mirrors Tick's own
// cancellation contract.
func TestSnapshot_ReturnsContextErrorWithoutReadingTheInbox(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})
	tr := &trim.Transport{Inbox: inbox}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tr.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

func writeYAML[T any](t *testing.T, dir, name string, v T) {
	t.Helper()
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), out, 0o600); err != nil {
		t.Fatal(err)
	}
}
