package thump

import (
	"context"
)

// Notifier delivers a held action to wherever a human watches. The concrete
// client (Slack, PagerDuty, …) lives in its own adapter package, injected
// here like Exec — internal/thump never imports an SDK directly.
type Notifier interface {
	Notify(ctx context.Context, h HeldAction) error
}
