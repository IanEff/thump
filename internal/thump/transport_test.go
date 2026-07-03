package thump_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/thump"
)

func TestTick_GoldenRun_OneEnvelopeInOrderAndOutcomeOut(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())
	tr := newTestTransport(inbox, outbox)

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("golden run must not error:", err)
	}

	// the rendered order IS the dry-run deliverable — the file a human
	// reads to see exactly what thump would have done. Round-trip included.
	if diff := cmp.Diff(goldenOrder(), readOneOrder(t, outbox)); diff != "" {
		t.Error("order drifted across the wire (-want +got)", diff)
	}
	if diff := cmp.Diff(goldenOutcome(), readOneOutcome(t, outbox)); diff != "" {
		t.Error("outcome drifted across the wire (-want +got)", diff)
	}
	// the input was archived, not deleted and not left to be re-acted-on
	if _, err := os.Stat(filepath.Join(inbox, "processed", "gov-001.yaml")); err != nil {
		t.Error("processed envelope must move to processed/, not vanish:", err)
	}
	// one Execute means one ledger record — the wiring half of Claim 11
	if n := len(tr.Log.ByResult(thump.ResultRendered)); n != 1 {
		t.Errorf("one envelope must mean one ledger record, got %d", n)
	}
}

func TestTick_SkipsTheUnapprovedLoudly(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	writeGovernedYAML(t, inbox, "gov-esc.yaml", escalatedGoverned())
	tr := newTestTransport(inbox, outbox)

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("a skip must not fail the pass:", err)
	}

	// NOTHING was rendered and NOTHING was recorded — I-10's other half:
	// an unapproved decision doesn't produce a quieter act; it produces none.
	if got, _ := filepath.Glob(filepath.Join(outbox, "orders", "*.yaml")); len(got) != 0 {
		t.Errorf("an escalated decision must render no order, found %v", got)
	}
	if got, _ := filepath.Glob(filepath.Join(outbox, "outcomes", "*.yaml")); len(got) != 0 {
		t.Errorf("an escalated decision must produce no outcome, found %v", got)
	}
	if n := len(tr.Log.Since(time.Time{})); n != 0 {
		t.Errorf("the ledger records acts, not declinations: got %d records", n)
	}
	// …but the envelope went somewhere a human can audit, not into the void
	if _, err := os.Stat(filepath.Join(inbox, "skipped", "gov-esc.yaml")); err != nil {
		t.Error("an unapproved envelope must land in skipped/, not vanish:", err)
	}
	// and the loop is clean — a second pass over the emptied inbox is a no-op
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own skip:", err)
	}
}

func TestTick_PoisonPill_QuarantinesAndSurvives(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	writeGovernedYAML(t, inbox, "good.yaml", approvedGoverned())
	if err := os.WriteFile(filepath.Join(inbox, "poison.yaml"),
		[]byte("decision: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newTestTransport(inbox, outbox)

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("one bad file must not fail the pass:", err)
	}

	// the good envelope still got acted on — the poison didn't block the queue
	if got := readOneOutcome(t, outbox); got.Result != thump.ResultRendered {
		t.Errorf("the healthy envelope must still be rendered: %+v", got)
	}
	// the poison is quarantined where a human can find it, not deleted
	if _, err := os.Stat(filepath.Join(inbox, "quarantine", "poison.yaml")); err != nil {
		t.Error("unparseable input must land in quarantine/:", err)
	}
	// and the loop SURVIVES
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own quarantine:", err)
	}
}
