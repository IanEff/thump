// Package slack delivers a held action to a Slack incoming webhook — the
// concrete Notifier thump injects at wiring time, kept in its own package so
// the HTTP client lives outside the reasoning/rendering beat, never inside it.
// It posts a one-line digest of the hold, never the raw proposal.Set.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ianeff/thump/internal/thump"
)

// ErrWebhookStatus is returned when Slack answers a post with a non-2xx
// status — the digest was built and sent, but delivery was refused, and that's
// reported rather than swallowed.
var ErrWebhookStatus = errors.New("slack: webhook returned non-2xx status")

// Webhook posts a held action's digest to a Slack incoming-webhook URL.
type Webhook struct {
	URL    string       // the incoming-webhook endpoint — the whole secret; treat it like a credential
	Client *http.Client // nil uses a 10s-timeout default; inject one to point tests at an httptest server
}

// Notify posts the held action's digest to the webhook. A transport error or a
// non-2xx status (ErrWebhookStatus) is returned to the caller — which treats
// delivery as best-effort, so a failed notify logs but never blocks the hold.
func (w *Webhook) Notify(ctx context.Context, h thump.HeldAction) error {
	body, err := json.Marshal(map[string]string{"text": digest(h)})
	if err != nil {
		return fmt.Errorf("slack: marshal held action: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client().Do(req)
	if err != nil {
		return fmt.Errorf("slack: post to webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%w: %s", ErrWebhookStatus, resp.Status)
	}
	return nil
}

func (w *Webhook) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// digest renders the single line a human reads to bless-or-kill: which action
// held, on what, and why — drawn from the Decision and the Set's recommended
// Candidate, never the raw evidence.
func digest(h thump.HeldAction) string {
	return fmt.Sprintf("held for review: %s (%s, %s) — risk %s over the auto-fire ceiling [%s]; signal %s",
		h.Set.ContractRefFor(h.Decision.CandidateRef),
		h.Set.FailureClass,
		h.Set.ServiceTier,
		h.Decision.RiskBand,
		strings.Join(h.Decision.Reasons, ", "),
		h.Decision.SignalRef,
	)
}
