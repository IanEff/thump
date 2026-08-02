package thump

import (
	"context"

	"github.com/ianeff/thump/api/v1/decision"
)

// Notifier delivers a held action to wherever a human watches. The concrete
// client (Slack, PagerDuty, …) lives in its own adapter package, injected
// here like Exec — internal/thump never imports an SDK directly.
type Notifier interface {
	Notify(ctx context.Context, h decision.Governed) error
}
