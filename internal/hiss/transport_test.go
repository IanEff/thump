package hiss_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/decision"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/proposal"
	"sigs.k8s.io/yaml"
)

func TestTick_GoldenRun_OneSetInOneDecisionOut(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	writeSetYAML(t, inbox, "ps-001.yaml", governedSet())

	tr := newTestTransport(inbox, outbox)

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("golden run must not error: ", err)
	}

	got := readOneGoverned(t, outbox)

	if diff := cmp.Diff(goldenDecision(), got.Decision); diff != "" {
		t.Error("decision drifted across the wire (-want +got)", diff)
	}
	if diff := cmp.Diff(governedSet(), got.Set); diff != "" {
		t.Error("hiss must seal the set into the envelope verbatim (-want, +got)", diff)
	}
	if _, err := os.Stat(filepath.Join(inbox, "processed", "ps-001.yaml")); err != nil {
		t.Error("processed input must move to processed/, not vanish:", err)
	}
	if n := len(tr.Log.ByVerdict(hiss.VerdictApproved)); n != 1 {
		t.Errorf("one Evaluate must mean one ledger record, got %d", n)
	}
}

func TestTick_PoisonPill_QuarantinesAndSurvives(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	writeSetYAML(t, inbox, "good.yaml", governedSet())
	if err := os.WriteFile(filepath.Join(inbox, "poison.yaml"),
		[]byte("proposals: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newTestTransport(inbox, outbox)

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("one bad file must not fail the pass:", err)
	}

	// the good file still got decided — the poison didn't block the queue
	if got := readOneGoverned(t, outbox); got.Decision.Verdict != hiss.VerdictApproved {
		t.Errorf("the healthy set must still be decided: %+v", got)
	}
	// the poison is quarantined where a human can find it, not deleted
	if _, err := os.Stat(filepath.Join(inbox, "quarantine", "poison.yaml")); err != nil {
		t.Error("unparseable input must land in quarantine/:", err)
	}
	// and the loop SURVIVES — a second pass over the now-clean inbox is a no-op
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own quarantine:", err)
	}
}

func writeSetYAML(t *testing.T, dir, name string, ps proposal.Set) {
	t.Helper()
	out, err := yaml.Marshal(ps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readOneGoverned(t *testing.T, outbox string) decision.Governed {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(outbox, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("outbox must hold exactly one envelope, found %d: %v", len(matches), matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var g decision.Governed
	if err := yaml.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func newTestTransport(inbox, outbox string) *hiss.Transport {
	return &hiss.Transport{
		Inbox:  inbox,
		Outbox: outbox,
		Policy: calmPolicy(),
		Log:    hiss.NewDecisionLog(),
		Now:    frozenNow,
	}
}
