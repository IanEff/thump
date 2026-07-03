package clank_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/outcome"
	"sigs.k8s.io/yaml"
)

func TestAbsorb_RefusesTheUnauditable(t *testing.T) {
	t.Parallel()
	c := newClick(t)
	o := liveSuccess()
	o.ExecutedAt = time.Time{}

	err := c.Absorb(context.Background(), o)

	if !errors.Is(err, clank.ErrUnauditableOutcome) {
		t.Errorf("an unauditable outcome must never be learned from. got %v", err)
	}
	if got := len(c.Cases.Cases("slo_burn:ceph-rgw")); got != 0 {
		t.Errorf("a refused outcome must bank no case, got %d", got)
	}
}

func TestAbsorb_RefusesAnIncoherentOutcome(t *testing.T) {
	t.Parallel()
	cases := map[string]func(o *outcome.Outcome){
		"Absorb refuses a rehearsal that claims success": func(o *outcome.Outcome) {
			o.Mode, o.Result = outcome.ModeDryRun, outcome.ResultSuccess
		},
		"Absorb refuses a rehearsal that claims failure": func(o *outcome.Outcome) {
			o.Mode, o.Result = outcome.ModeDryRun, outcome.ResultFailure
			o.Error = "a rehearsal cannot fail at reality"
		},
		"Absorb refuses a live act that claims it only rendered": func(o *outcome.Outcome) {
			o.Mode, o.Result = outcome.ModeLive, outcome.ResultRendered
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newClick(t)
			o := liveSuccess()
			breakIt(&o)

			if err := c.Absorb(context.Background(), o); !errors.Is(err, clank.ErrIncoherentOutcome) {
				t.Errorf("mode and result must agree before anyone learns anything, got %v", err)
			}
		})
	}
}

func TestAbsorb_GoldenPath_BookkeepsTheWholeLoop(t *testing.T) {
	t.Parallel()
	c := newClick(t)

	if err := c.Absorb(context.Background(), liveSuccess()); err != nil {
		t.Fatal("the golden outcome must absorb:", err)
	}

	// leg one: the lifecycle closed on the set that started all this…
	open, err := c.Ledger.Open(context.Background(), "slo_burn:ceph-rgw", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("a closed set must leave the open pile, still open: %d", len(open))
	}
	// …leg two: the case base banked exactly the golden case — including
	// the STATED 0.87, married to its observed result at last.
	if diff := cmp.Diff([]clank.Case{goldenCase()}, c.Cases.Cases("slo_burn:ceph-rgw")); diff != "" {
		t.Error("the banked case drifted from the golden fixture (-want +got)", diff)
	}
}

func newClick(t *testing.T) clank.Click {
	t.Helper()
	return clank.Click{Ledger: seededLedger(t), Cases: clank.NewCaseBase()}
}

func TestTick_GoldenRun_OneOutcomeInOneCaseBanked(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeOutcomeYAML(t, inbox, "out-001.yaml", liveSuccess())
	re := newReturnEdge(t, inbox)

	if err := re.Tick(context.Background()); err != nil {
		t.Fatal("golden run must not error:", err)
	}

	if diff := cmp.Diff([]clank.Case{goldenCase()}, re.Click.Cases.Cases("slo_burn:ceph-rgw")); diff != "" {
		t.Error("case drifted across the wire (-want +got)", diff)
	}
	if _, err := os.Stat(filepath.Join(inbox, "processed", "out-001.yaml")); err != nil {
		t.Error("an absorbed outcome must move to processed/, not vanish:", err)
	}
	// and the loop is clean — a second pass over the emptied inbox is a no-op
	if err := re.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own success:", err)
	}
}

func TestTick_AnOrphanOutcomeLandsInUnmatched(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeOutcomeYAML(t, inbox, "orphan.yaml", liveSuccess())
	re := newReturnEdge(t, inbox)
	re.Click.Ledger = clank.NewMemProposalLog() // empty — nobody proposed anything here

	if err := re.Tick(context.Background()); err != nil {
		t.Fatal("an orphan must not fail the pass:", err)
	}

	if _, err := os.Stat(filepath.Join(inbox, "unmatched", "orphan.yaml")); err != nil {
		t.Error("an unanswerable outcome must land in unmatched/ for replay:", err)
	}
	if got := len(re.Click.Cases.Cases("slo_burn:ceph-rgw")); got != 0 {
		t.Errorf("no open set means no case — got %d banked from an orphan", got)
	}
	if err := re.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own orphan:", err)
	}
}

func TestTick_PoisonPill_QuarantinesAndSurvives(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeOutcomeYAML(t, inbox, "good.yaml", liveSuccess())
	if err := os.WriteFile(filepath.Join(inbox, "poison.yaml"),
		[]byte("result: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	re := newReturnEdge(t, inbox)

	if err := re.Tick(context.Background()); err != nil {
		t.Fatal("one bad file must not fail the pass:", err)
	}

	// the good outcome was still absorbed — the poison didn't block the queue
	if got := len(re.Click.Cases.Cases("slo_burn:ceph-rgw")); got != 1 {
		t.Errorf("the healthy outcome must still be absorbed, got %d cases", got)
	}
	// the poison is quarantined where a human can find it, not deleted
	if _, err := os.Stat(filepath.Join(inbox, "quarantine", "poison.yaml")); err != nil {
		t.Error("unparseable input must land in quarantine/:", err)
	}
	if err := re.Tick(context.Background()); err != nil {
		t.Fatal("the loop must survive its own quarantine:", err)
	}
}

func writeOutcomeYAML(t *testing.T, dir, name string, o outcome.Outcome) {
	t.Helper()
	out, err := yaml.Marshal(o) // sigs.k8s.io/yaml — thump's writing codec
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newReturnEdge(t *testing.T, inbox string) *clank.ReturnEdge {
	t.Helper()
	return &clank.ReturnEdge{Inbox: inbox, Click: newClick(t)}
}
