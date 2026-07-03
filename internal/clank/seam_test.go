package clank_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/clank"
	"github.com/ianeff/clank/internal/contract"
	"github.com/ianeff/clank/internal/decision"
	"github.com/ianeff/clank/internal/hiss"
	"github.com/ianeff/clank/internal/proposal"
	"github.com/ianeff/clank/internal/rattle"
	"github.com/ianeff/clank/internal/signal"
	"github.com/ianeff/clank/internal/thump"
)

// seamSource is a three-line in-process rattle.Source (the rattle test fakes live in
// package rattle_test and can't be imported here, so we spell it out).
type seamSource struct{ samples []rattle.Sample }

func (s seamSource) BurnSamples(context.Context, rattle.SLO) ([]rattle.Sample, error) {
	return s.samples, nil
}

// seamDetection runs a REAL rattle.Reconcile and returns its single Detection — the
// actual seam output, not a hand-built signal.Detection.
func seamDetection(t *testing.T) signal.Detection {
	t.Helper()
	base := time.Unix(0, 0)
	burn := []rattle.Sample{ // 1,2,4,8 → accelerating → fires the acceleration detector
		{T: base, BurnRate: 1},
		{T: base.Add(time.Minute), BurnRate: 2},
		{T: base.Add(2 * time.Minute), BurnRate: 4},
		{T: base.Add(3 * time.Minute), BurnRate: 8},
	}
	r := &rattle.Reconciler{
		SLOs: []rattle.SLO{{
			ID: "ceph-rgw-availability", Object: "ceph-rgw",
			Tier: "tier-1", ContractRef: "ceph-rgw-availability:v1", // Tier MUST match the catalog's ApplicableTiers
		}},
		Source:   seamSource{samples: burn},
		Detector: rattle.AccelerationDetector{Threshold: 0.5},
		Debounce: rattle.NewDebouncer(10 * time.Minute),
		Now:      func() time.Time { return time.Unix(1000, 0) },
	}
	dets, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("seam precondition: rattle.Reconcile errored: %v", err)
	}
	if len(dets) != 1 {
		t.Fatalf("seam precondition: want exactly 1 detection, got %d", len(dets))
	}
	return dets[0] // Fingerprint "slo_burn:ceph-rgw", ServiceTier "tier-1"
}

func TestSeam_RattleDetectionDrivesClankToADeliveredProposal(t *testing.T) {
	t.Parallel()
	det := seamDetection(t) // real rattle output

	// scripted clank model: step 1 investigate (live evidence → clears the gate),
	// step 2 propose a CATALOGUED action for the detection's class + tier.
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation, // must be in newTestEngine's catalog
			Hypotheses:   []clank.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}

	eng, sink := newTestEngine(model) // the EXACT engine the clank tests use
	set, err := eng.Propose(context.Background(), det)
	if err != nil {
		t.Fatal("clank must accept a real rattle Detection without error", err)
	}

	// the seam held: gated through, delivered once, and the fingerprint survived intact.
	if set.Gate == nil || !set.Gate.Passed {
		t.Errorf("gate rejected a well-formed seam detection: %+v", set.Gate)
	}
	if diff := cmp.Diff("proposed", set.Status.Phase); diff != "" {
		t.Error("delivered proposal should be phase=proposed (-want +got)", diff)
	}
	if len(sink.delivered) != 1 {
		t.Fatalf("a passed set is delivered exactly once; delivered %d", len(sink.delivered))
	}
	if diff := cmp.Diff("slo_burn:ceph-rgw", sink.delivered[0].SignalRef); diff != "" {
		t.Error("fingerprint didn't survive the seam (ProposalSet.SignalRef) (-want +got)", diff)
	}
}

func TestSeam_FourBeatsFromDetectionToDryRunOutcome(t *testing.T) {
	t.Parallel()
	// scripted model: step 1 investigate (live evidence clears the gate),
	// step 2 propose a catalogued, REVERSIBLE candidate that REQUESTS A BAND
	// (both trap dodges — see the banner above).
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation, // in newTestEngine's catalog
			Hypotheses:   []clank.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals: []clank.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87,
				ReversalPath: &clank.ReversalPath{ // trap 1: without this, hiss's I-12 veto fires
					Method: "unthrottle", Watching: "latency_p99", Trigger: "slo_recovery",
				},
				GovernanceLevel: &clank.GovernanceLevel{ // trap 2: without this, the grant is observe (D-3)
					Band: string(hiss.BandActReversible),
				},
			}},
		})}}},
	}}

	eng, sink := newTestEngine(model)
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal("clank leg of the seam errored:", err)
	}
	if len(sink.delivered) != 1 {
		t.Fatalf("seam precondition: want exactly 1 delivered set, got %d", len(sink.delivered))
	}

	// beat three: govern (seamPolicy() reused from hiss_seam_test.go —
	// MaxBand["tier-1"] = act_reversible, so the requested band clears it).
	var auth hiss.Authority
	dec := auth.Evaluate(sink.delivered[0], seamPolicy(), time.Unix(1000, 0))
	if diff := cmp.Diff(hiss.VerdictApproved, dec.Verdict); diff != "" {
		t.Fatalf("seam precondition: hiss must approve (-want +got)\n%s\nreasons: %v", diff, dec.Reasons)
	}

	// beat four: render + dry-run. The envelope is exactly what hiss's
	// transport would have sealed; here we seal it by hand (no filesystem
	// in this test — the seam is the types, not the transport).
	order, err := thump.Actuator{}.Render(
		decision.Governed{Decision: dec, Set: sink.delivered[0]},
		seamCatalog(), time.Unix(1000, 0))
	if err != nil {
		t.Fatal("thump leg of the seam errored:", err)
	}
	if diff := cmp.Diff(hiss.BandActReversible, order.GrantedBand); diff != "" {
		t.Error("the granted band didn't survive the seam — see the trap banner (-want +got)", diff)
	}

	out := thump.DryRun{}.Execute(context.Background(), order, time.Unix(1000, 0))
	if err := out.Auditable(); err != nil {
		t.Error("every outcome crossing the seam must be auditable:", err)
	}
	if diff := cmp.Diff(thump.ResultRendered, out.Result); diff != "" {
		t.Error("the four-beat happy line ends in a rehearsal, not an act (-want +got)", diff)
	}
	// the fingerprint survived detection → proposal → decision → OUTCOME:
	if diff := cmp.Diff(sigBurnAccel().Fingerprint, out.SignalRef); diff != "" {
		t.Error("fingerprint didn't survive four beats (-want +got)", diff)
	}
}

func seamCatalog() *contract.StaticCatalog {
	return contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		Action: contract.ActionSpec{
			Description:     "Throttle non-critical request paths at the ingress",
			ScopeParameters: map[string]contract.Range{"throttle_pct": {Min: 10, Max: 60, Default: 25}},
		},
		Reversal:        contract.Reversal{Method: "unthrottle", Fallback: "page-oncall"},
		SuccessCriteria: contract.SuccessCriteria{Metric: "latency_p99", Target: "p99 < 250ms", Window: 10 * time.Minute},
	}})
}

func TestSeam_FiveBeats_TheLoopClosesWithoutBelief(t *testing.T) {
	t.Parallel()
	// beats one and two: real detection, real engine, scripted model —
	// both trap dodges (ReversalPath + GovernanceLevel) inherited from the
	// four-beat seam, which explains them.
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Hypotheses:   []clank.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals: []clank.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87,
				ReversalPath: &clank.ReversalPath{
					Method: "unthrottle", Watching: "latency_p99", Trigger: "slo_recovery",
				},
				GovernanceLevel: &clank.GovernanceLevel{Band: string(hiss.BandActReversible)},
			}},
		})}}},
	}}

	eng, sink := newTestEngine(model)
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal("clank leg of the seam errored:", err)
	}
	if len(sink.delivered) != 1 {
		t.Fatalf("seam precondition: want exactly 1 delivered set, got %d", len(sink.delivered))
	}

	// beat three: govern.
	var auth hiss.Authority
	dec := auth.Evaluate(sink.delivered[0], seamPolicy(), time.Unix(1000, 0))
	if diff := cmp.Diff(hiss.VerdictApproved, dec.Verdict); diff != "" {
		t.Fatalf("seam precondition: hiss must approve (-want +got)\n%s\nreasons: %v", diff, dec.Reasons)
	}

	// beat four: render + rehearse.
	order, err := thump.Actuator{}.Render(
		decision.Governed{Decision: dec, Set: sink.delivered[0]},
		seamCatalog(), time.Unix(1000, 0))
	if err != nil {
		t.Fatal("thump leg of the seam errored:", err)
	}
	out := thump.DryRun{}.Execute(context.Background(), order, time.Unix(1000, 0))

	// beat five: the return edge — into the SAME ledger the engine
	// recorded its proposal in. This is the sentence the whole phase was
	// building toward: the loop is one process's state, closed.
	cb := clank.NewCaseBase()
	click := clank.Click{Ledger: eng.Ledger, Cases: cb}
	if err := click.Absorb(context.Background(), out); err != nil {
		t.Fatal("click leg of the seam errored:", err)
	}

	fp := sigBurnAccel().Fingerprint

	// the lifecycle leg: the engine's own set is now ACKNOWLEDGED — and
	// still open, so the incident it never actually fixed keeps deduping.
	open, err := eng.Ledger.Open(context.Background(), fp, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("the rehearsed set must stay open (it keeps deduping), got %d open", len(open))
	}
	if diff := cmp.Diff(proposal.PhaseAcknowledge, open[0].Status.Phase); diff != "" {
		t.Error("a rehearsal acknowledges the set it answers to (-want +got)", diff)
	}

	// the memory leg: one case banked, fingerprint intact five beats out,
	// the STATED 0.87 married to its outcome — CE's raw material, banked.
	cases := cb.Cases(fp)
	if len(cases) != 1 {
		t.Fatalf("one outcome must mean one case, got %d", len(cases))
	}
	if diff := cmp.Diff(fp, cases[0].Fingerprint); diff != "" {
		t.Error("fingerprint didn't survive five beats (-want +got)", diff)
	}
	if diff := cmp.Diff(0.87, cases[0].Confidence); diff != "" {
		t.Error("the stated confidence must be banked at absorb time (-want +got)", diff)
	}

	// and the machine BELIEVES nothing new: a rehearsal is bookkeeping,
	// not evidence. The prior the next cycle scores with is untouched.
	if rate, corroborated := cb.Alignment(fp); corroborated {
		t.Errorf("the loop closed on a rehearsal — nothing may be believed yet (rate %v)", rate)
	}
}
