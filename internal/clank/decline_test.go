package clank_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/clank"
	"sigs.k8s.io/yaml"
)

func TestTick_GoldenRun_OneDeclineClosesTheLedgerRow(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeDeclineYAML(t, inbox, "dec-001.yaml", escalatedDecision())
	ledger := seededLedger(t) // one clickSet() recorded, phase proposed
	de := &clank.DeclineEdge{Inbox: inbox, Ledger: ledger}

	if err := de.Tick(context.Background()); err != nil {
		t.Fatal("golden run must not error:", err)
	}

	open, err := ledger.Open(context.Background(), "slo_burn:ceph-rgw", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("a declined set must leave the open pile: %d still open", len(open))
	}
	if _, err := os.Stat(filepath.Join(inbox, "processed", "dec-001.yaml")); err != nil {
		t.Error("a closed decline must move to processed/, not vanish:", err)
	}
	// and the loop is clean — a second pass over the emptied inbox is a no-op
	if err := de.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own success:", err)
	}
}

func TestTick_AnOrphanDeclineLandsInUnmatched(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeDeclineYAML(t, inbox, "orphan.yaml", escalatedDecision())
	de := &clank.DeclineEdge{Inbox: inbox, Ledger: clank.NewMemProposalLog()} // empty — nobody proposed anything here

	if err := de.Tick(context.Background()); err != nil {
		t.Fatal("an orphan must not fail the pass:", err)
	}

	if _, err := os.Stat(filepath.Join(inbox, "unmatched", "orphan.yaml")); err != nil {
		t.Error("an unanswerable decline must land in unmatched/ for replay:", err)
	}
	if err := de.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own orphan:", err)
	}
}

func TestTick_DeclinePoisonPill_QuarantinesAndSurvives(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeDeclineYAML(t, inbox, "good.yaml", escalatedDecision())
	if err := os.WriteFile(filepath.Join(inbox, "poison.yaml"),
		[]byte("verdict: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger := seededLedger(t)
	de := &clank.DeclineEdge{Inbox: inbox, Ledger: ledger}

	if err := de.Tick(context.Background()); err != nil {
		t.Fatal("one bad file must not fail the pass:", err)
	}

	// the good decline still closed the ledger row — the poison didn't block the queue
	open, err := ledger.Open(context.Background(), "slo_burn:ceph-rgw", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("the healthy decline must still have closed the row: %d still open", len(open))
	}
	// the poison is quarantined where a human can find it, not deleted
	if _, err := os.Stat(filepath.Join(inbox, "quarantine", "poison.yaml")); err != nil {
		t.Error("unparseable input must land in quarantine/:", err)
	}
	if err := de.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own quarantine:", err)
	}
}

func escalatedDecision() decision.Decision {
	return decision.Decision{
		ID:            "dec:slo_burn:ceph-rgw:2000",
		ProposalRef:   "ps-ceph-rgw-001", // == clickSet().Name
		SignalRef:     "slo_burn:ceph-rgw",
		CandidateRef:  "p1",
		Verdict:       decision.VerdictEscalate,
		Reasons:       []string{decision.ReasonConfidenceFloor},
		RequestedBand: decision.BandActReversible,
		PolicyVersion: "policy-v1",
		EvaluatedAt:   time.Unix(2000, 0),
	}
}

func writeDeclineYAML(t *testing.T, dir, name string, d decision.Decision) {
	t.Helper()
	out, err := yaml.Marshal(d) // sigs.k8s.io/yaml — thump's writing codec
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), out, 0o600); err != nil {
		t.Fatal(err)
	}
}
