package slack_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/notify/slack"
	"github.com/ianeff/thump/internal/thump"
)

func TestWebhookNotify_PostsTheHeldDigestToTheWebhook(t *testing.T) {
	t.Parallel()

	var got struct {
		method string
		ctype  string
		text   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.ctype = r.Header.Get("Content-Type")
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("webhook received undecodable body: %v", err)
		}
		got.text = body.Text
	}))
	defer srv.Close()

	wh := &slack.Webhook{URL: srv.URL}
	if err := wh.Notify(context.Background(), heldAction()); err != nil {
		t.Fatal("Notify to a healthy webhook must succeed:", err)
	}

	wantText := "held for review: accelerate-recovery (redundancy_degraded, tier-1) — " +
		"risk act_disruptive over the auto-fire ceiling [risk_ceiling]; signal slo_burn:ceph-data"
	if diff := cmp.Diff(http.MethodPost, got.method); diff != "" {
		t.Error("webhook posted with the wrong method", diff)
	}
	if diff := cmp.Diff("application/json", got.ctype); diff != "" {
		t.Error("webhook posted with the wrong content type", diff)
	}
	if diff := cmp.Diff(wantText, got.text); diff != "" {
		t.Error("posted digest wrong", diff)
	}
}

func TestWebhookNotify_ReturnsErrWebhookStatusWhenSlackRejects(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh := &slack.Webhook{URL: srv.URL}
	err := wh.Notify(context.Background(), heldAction())
	if !errors.Is(err, slack.ErrWebhookStatus) {
		t.Errorf("a non-2xx webhook response must return ErrWebhookStatus, got %v", err)
	}
}

// heldAction is a redundancy_degraded hold whose recommended Candidate is the
// high-blast accelerate-recovery action — the shape a live hold delivers.
func heldAction() thump.HeldAction {
	return thump.HeldAction{
		Decision: decision.Decision{
			SignalRef:    "slo_burn:ceph-data",
			CandidateRef: "p1",
			Verdict:      decision.VerdictHold,
			Reasons:      []string{decision.ReasonRiskCeiling},
			RiskBand:     decision.BandActDisruptive,
		},
		Set: proposal.Set{
			SignalRef:    "slo_burn:ceph-data",
			FailureClass: proposal.ClassRedundancyDegraded,
			ServiceTier:  "tier-1",
			Recommended:  "p1",
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "accelerate-recovery"}},
		},
	}
}
