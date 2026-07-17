package thump_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
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
	if n := len(tr.Log.ByResult(outcome.ResultRendered)); n != 1 {
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
	// clank still needs to know: a decline notice closes its ledger's dedup
	// window without ever going through Outcome/Click/the case base.
	if diff := cmp.Diff(escalatedGoverned().Decision, readOneDecline(t, outbox)); diff != "" {
		t.Error("decline drifted across the wire (-want +got)", diff)
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
	if got := readOneOutcome(t, outbox); got.Result != outcome.ResultRendered {
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

func TestHandle_FiresAnAutomaticReversalAfterALiveForwardOrderFailsToConverge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inbox, outbox := t.TempDir(), t.TempDir()
		writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())

		runner := &fakeRunner{}
		tr := newTestTransport(inbox, outbox)
		tr.Exec = thump.Live{Runner: runner}
		tr.Reversal = &thump.ReversalWatcher{
			Probe: thump.PrometheusConverger{Probe: &fakeProbe{answer: false}}, // never converges
			Now:   frozenNow,
		}

		if err := tr.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()                          // let watchAndSettle's goroutine reach its timer block
		time.Sleep(goldenOrder().Success.Window) // every goroutine now blocked on a timer -> fake clock jumps
		synctest.Wait()                          // let watchAndSettle finish its post-timer work before we assert

		if !runner.called || !runner.gotReverse {
			// note: fakeRunner only remembers the LAST call — you may need a
			// small recording slice here instead if you want to assert BOTH
			// the forward and the reversal call happened, in order
			t.Error("an unconverged live order must run its authored undo")
		}
	})
}

func TestTick_HoldsAndNotifiesButKeepsTheLock(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	writeGovernedYAML(t, inbox, "gov-hold.yaml", heldGoverned())
	tr := newTestTransport(inbox, outbox)
	notifier := &fakeNotifier{}
	tr.Notifier = notifier

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("a hold must not fail the pass:", err)
	}

	if diff := cmp.Diff(heldGoverned().Decision, readOneHeld(t, outbox).Decision); diff != "" {
		t.Error("held decision drifted across the wire (-want +got)", diff)
	}
	if got, _ := filepath.Glob(filepath.Join(outbox, "declines", "*.yaml")); len(got) != 0 {
		t.Errorf("a hold must not free the dedupe lock via a decline, found %v", got)
	}
	if got, _ := filepath.Glob(filepath.Join(outbox, "orders", "*.yaml")); len(got) != 0 {
		t.Errorf("a hold must not fire anything, found order(s) %v", got)
	}
	if _, err := os.Stat(filepath.Join(inbox, "skipped", "gov-hold.yaml")); err != nil {
		t.Error("a held envelope must land in skipped/, not vanish:", err)
	}
	if len(notifier.notified) != 1 {
		t.Fatalf("want exactly one Notify call, got %d", len(notifier.notified))
	}
	if diff := cmp.Diff(heldGoverned().Decision, notifier.notified[0].Decision); diff != "" {
		t.Error("the notified HeldAction's decision drifted from the held envelope (-want +got)", diff)
	}
}

// TestTick_HoldSurvivesANotifierFailure pins the degrade-gracefully contract
// the guide's Done-when line names: a Notifier is best-effort delivery, not
// a gate — its error is logged, never propagated, and the hold still lands
// where a human can find it.
func TestTick_HoldSurvivesANotifierFailure(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	writeGovernedYAML(t, inbox, "gov-hold.yaml", heldGoverned())
	tr := newTestTransport(inbox, outbox)
	tr.Notifier = &fakeNotifier{err: errors.New("slack: 503")}

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal("a notifier failure must not fail the pass:", err)
	}
	if _, err := os.Stat(filepath.Join(inbox, "skipped", "gov-hold.yaml")); err != nil {
		t.Error("a held envelope must still land in skipped/ despite the notifier failing:", err)
	}
}

func heldGoverned() decision.Governed {
	g := approvedGoverned()
	g.Decision.Verdict = decision.VerdictHold
	g.Decision.Reasons = []string{decision.ReasonRiskCeiling}
	g.Decision.RiskBand = decision.BandActDisruptive
	g.Decision.GrantedBand = ""
	return g
}
