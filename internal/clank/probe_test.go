package clank_test

import (
	"testing"

	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/evidence"
)

// TestProbeEngine_NeverWiresAPublisherOrJournal is the structural half of
// calipers probe's read-only guarantee: a probe run can't reach the broker
// or the corpus not because a caller remembered to leave Pub/Journal unset,
// but because ProbeEngine never sets them at all.
func TestProbeEngine_NeverWiresAPublisherOrJournal(t *testing.T) {
	t.Parallel()

	eng, err := clank.ProbeEngine(config.Clank{}, nil, evidence.Config{}, nil, nil, nil, nil, nil, clank.ScoringWeights{}, clank.Limits{})
	if err != nil {
		t.Fatalf("ProbeEngine errored: %v", err)
	}
	if eng.Pub != nil {
		t.Error("ProbeEngine wired a Pub — a probe run must never be able to reach the broker")
	}
	if eng.Journal != nil {
		t.Error("ProbeEngine wired a Journal — a probe run must never be able to reach the corpus")
	}
}

// TestProbeReset_GivesEachCallAFreshLedgerAndStore pins the property
// probe.Run depends on to avoid a false dedup collision across repeated
// Propose calls against the same fingerprint: every call gets its own,
// previously-untouched Ledger and Store.
func TestProbeReset_GivesEachCallAFreshLedgerAndStore(t *testing.T) {
	t.Parallel()

	eng, err := clank.ProbeEngine(config.Clank{}, nil, evidence.Config{}, nil, nil, nil, nil, nil, clank.ScoringWeights{}, clank.Limits{})
	if err != nil {
		t.Fatalf("ProbeEngine errored: %v", err)
	}

	clank.ProbeReset(eng)
	firstLedger, firstStore := eng.Ledger, eng.Store
	if firstLedger == nil || firstStore == nil {
		t.Fatal("ProbeReset left Ledger or Store nil")
	}

	clank.ProbeReset(eng)
	if eng.Ledger == firstLedger {
		t.Error("ProbeReset reused the previous call's Ledger")
	}
	if eng.Store == firstStore {
		t.Error("ProbeReset reused the previous call's Store")
	}
}
