package thump

import (
	"context"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/config"
)

// HandleForTest exposes Transport.handle to thump_test without handle
// becoming part of thump's real API. Only compiled under `go test` — the
// _test.go suffix keeps it out of the shipped binary. Mirrors
// internal/hiss/export_test.go, internal/clank/export_test.go, and
// internal/rattle/export_test.go.
func (tr *Transport) HandleForTest(ctx context.Context, g decision.Governed, heartbeat func()) error {
	return tr.handle(ctx, g, heartbeat)
}

// BuildNotifierForTest exposes buildNotifier to thump_test — the seam
// W0b's silent-fallback warning hangs off.
func BuildNotifierForTest(cfg config.Thump, ctor func(url string) Notifier) Notifier {
	return buildNotifier(cfg, ctor)
}

// RenderNotesForTest exposes renderNotes to thump_test, so its rendering
// rules can be pinned directly against a hand-built Set rather than only
// indirectly through Actuator.Render's golden fixture.
func RenderNotesForTest(ps proposal.Set) string {
	return renderNotes(ps)
}
