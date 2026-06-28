package clank_test

import (
	"testing"

	"github.com/ianeff/clank/internal/clank"
)

func TestRanker_AcceleratingBlastFavorsFastContainment(t *testing.T) {
	t.Parallel()
	fast := clank.Candidate{ID: "throttle", PredictedImpact: impactRecovering("4-6m")}
	safe := clank.Candidate{ID: "rollback", PredictedImpact: impactRecovering("15m")}

	ranked, why := clank.NewRanker().Rank([]clank.Candidate{safe, fast}, "accelerating")
	if ranked[0].ID != "throttle" {
		t.Errorf("accelerating blast should rank fast containment first: got %s", ranked[0].ID)
	}
	if why.DominantAxis != "time_to_effect" {
		t.Errorf("rationale should record the dominant axis: got %s", why.DominantAxis)
	}
}

func impactRecovering(duration string) clank.PredictedImpact {
	effects := map[string]string{"time_to_effect": duration}
	return clank.PredictedImpact{SLOEffects: effects}
}
