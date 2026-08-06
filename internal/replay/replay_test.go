package replay_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/replay"
)

func fixture(t *testing.T, name string) replay.Transcript {
	t.Helper()
	tr, err := replay.LoadTranscript(
		filepath.Join("testdata", name+".jsonl"),
		filepath.Join("testdata", name+".set.json"))
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestModel_RefusesRatherThanImprovisingWhenTheTranscriptRunsShort pins the
// failure mode that matters. A truncated transcript is the common case, not
// the exotic one — segments were being cut off at shutdown until the drain
// was fixed.
func TestModel_RefusesRatherThanImprovisingWhenTheTranscriptRunsShort(t *testing.T) {
	t.Parallel()

	tr := fixture(t, "truncated")
	if _, err := replay.Propose(t.Context(), tr, clank.DefaultScoringWeights()); !errors.Is(err, replay.ErrTranscriptExhausted) {
		t.Error("want ErrTranscriptExhausted", err)
	}
}

// TestTools_ReconstructAnEvidenceRefTheGroundingTiersCountTheSameWayLiveDid
// is the claim the whole track rests on. Confidence is a product of
// grounding tier, and the tier is counted from EvidenceRef.Tool, .Live and
// .Subject — a replay whose refs lose any of the three re-scores every run
// at a tier it never earned.
func TestTools_ReconstructAnEvidenceRefTheGroundingTiersCountTheSameWayLiveDid(t *testing.T) {
	t.Parallel()

	tr := fixture(t, "slo_burn-cephblockpool")
	tools := replay.BuildTools(tr.Set)
	if len(tools) == 0 {
		t.Fatal("the recorded set carried no evidence — the fixture is not usable")
	}

	var got []proposal.EvidenceRef
	for name, tool := range tools {
		for {
			ref, err := tool.Run(t.Context(), nil)
			if errors.Is(err, replay.ErrTranscriptExhausted) {
				break
			}
			if err != nil {
				t.Fatalf("tool %s: %v", name, err)
			}
			got = append(got, ref)
		}
	}

	for _, ref := range got {
		if ref.Tool == "" {
			t.Error("replayed ref lost its Tool — the two-backend floor counts on it", ref.Query)
		}
		if ref.Query == "" {
			t.Error("replayed ref lost its Query — Citations match on it exactly", ref.Tool)
		}
	}
	if !hasLive(got) {
		t.Error("no replayed ref is Live — the gate's evidence minimum can never pass")
	}
}

func hasLive(refs []proposal.EvidenceRef) bool {
	for _, r := range refs {
		if r.Live {
			return true
		}
	}
	return false
}

// TestPropose_ReproducesTheSetTheRecordedRunEmitted is the only test in the
// tree that can assert the reason loop is deterministic given its inputs,
// because it is the only one holding a real run's inputs.
func TestPropose_ReproducesTheSetTheRecordedRunEmitted(t *testing.T) {
	t.Parallel()

	tr := fixture(t, "slo_burn-cephblockpool")
	got, err := replay.Propose(t.Context(), tr, clank.DefaultScoringWeights())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(tr.Set.FailureClass, got.FailureClass); diff != "" {
		t.Error("the replayed run reached a different failure class", diff)
	}
	if len(tr.Set.Proposals) > 0 && len(got.Proposals) > 0 {
		if diff := cmp.Diff(tr.Set.Proposals[0].Citations, got.Proposals[0].Citations); diff != "" {
			t.Error("the replayed run cited different evidence", diff)
		}
	}
}

// TestPropose_ReScoresTheSameRecordedRunUnderTwoWeightSets is the property
// the whole tuning track rests on: scoreConfidences runs after the model, so
// changing ScoringWeights changes the emitted number without changing
// anything the model saw. Citations must be identical and confidence must
// not be — anything else means the sweep is measuring noise.
func TestPropose_ReScoresTheSameRecordedRunUnderTwoWeightSets(t *testing.T) {
	t.Parallel()

	tr := fixture(t, "slo_burn-cephblockpool")

	base := clank.DefaultScoringWeights()
	lowered := base
	lowered.GroundingOne = 0.4

	a, err := replay.Propose(t.Context(), tr, base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := replay.Propose(t.Context(), tr, lowered)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Proposals) == 0 || len(b.Proposals) == 0 {
		t.Fatal("a declined set carries no candidate — this fixture cannot grade a weight change")
	}

	if diff := cmp.Diff(a.Proposals[0].Citations, b.Proposals[0].Citations); diff != "" {
		t.Error("a weight change altered what the run cited — the sweep is not measuring the knob", diff)
	}
	if a.Proposals[0].ComputedConfidence == b.Proposals[0].ComputedConfidence {
		t.Error("lowering GroundingOne changed nothing — the term is not reachable on this row")
	}
}
