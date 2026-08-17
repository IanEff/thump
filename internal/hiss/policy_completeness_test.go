package hiss_test

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/configtest"
	"github.com/ianeff/thump/internal/hiss"
)

// TestPolicy_FloorsCoverEveryActuatableClass is the confidence-floor
// completeness guard: a (tier, class) pair the actuator can really fire
// with no Policy.Floors entry clears hiss's confidence-floor veto on any
// nonzero Confidence at all, leaning on the reasoner's judgment instead of
// a real minimum (I-6). Keyed off actuate.BoundRefs, not the full catalog —
// a catalogued-but-unbound ref can't hurt anyone yet (that gap is G4a's).
func TestPolicy_FloorsCoverEveryActuatableClass(t *testing.T) {
	t.Parallel()
	pol, err := hiss.LoadPolicy(filepath.Join("..", "..", "config", "hiss", "policy.yaml"))
	if err != nil {
		t.Fatalf("load shipped policy: %v", err)
	}
	cat := configtest.ShippedCatalog(t)

	bound, err := actuate.BoundRefs(cat)
	if err != nil {
		t.Fatalf("the shipped catalog holds an action with no executable mechanism: %v", err)
	}

	var missing []string
	for _, ref := range bound {
		c, ok := cat.ByName(ref)
		if !ok {
			t.Fatalf("actuate.BoundRefs names %q, but the shipped catalog has no such contract", ref)
		}
		for _, tier := range c.ApplicableTiers {
			for _, class := range c.ApplicableFailureClasses {
				if pol.Floors[tier][class] <= 0 {
					missing = append(missing, tier+"/"+string(class)+" (via "+ref+")")
				}
			}
		}
	}

	if diff := cmp.Diff([]string(nil), missing); diff != "" {
		t.Error("actuatable classes with no confidence floor (-want +got):\n", diff)
	}
}

// TestPolicy_UnrecognisedConfidenceGateFailsClosedToCollapsed pins that any
// policy whose confidenceGate is absent, empty, or unrecognised evaluates
// identically to "collapsed" — an unrecognised gate value fails closed to
// today's single-veto behaviour, never open to split auto-approval.
func TestPolicy_UnrecognisedConfidenceGateFailsClosedToCollapsed(t *testing.T) {
	t.Parallel()

	gates := map[string]string{
		"empty string defaults to collapsed gate":       "",
		"explicit collapsed string is collapsed gate":   "collapsed",
		"unrecognised string fails closed to collapsed": "split_custom",
	}

	for name, gate := range gates {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ps := governedSet()
			ps.Proposals[0].ComputedConfidence = 1.00
			ps.Proposals[0].Confidence = 0.65 // below calmPolicy's 0.75 floor

			pol := calmPolicy()
			pol.ConfidenceGate = gate

			got := decide(t, ps, pol)
			if diff := cmp.Diff(decision.VerdictEscalate, got.Verdict); diff != "" {
				t.Errorf("unrecognised gate value %q must escalate (-want +got):\n%s", gate, diff)
			}
			if diff := cmp.Diff([]string{hiss.ReasonConfidenceFloor}, got.Reasons); diff != "" {
				t.Errorf("unrecognised gate value %q must cite ReasonConfidenceFloor (-want +got):\n%s", gate, diff)
			}
		})
	}
}
