package thump

import (
	"context"

	"github.com/ianeff/thump/api/v1/decision"
)

// HandleForTest exposes Transport.handle to thump_test without handle
// becoming part of thump's real API. Only compiled under `go test` — the
// _test.go suffix keeps it out of the shipped binary. Mirrors
// internal/hiss/export_test.go, internal/clank/export_test.go, and
// internal/rattle/export_test.go.
func (tr *Transport) HandleForTest(ctx context.Context, g decision.Governed) error {
	return tr.handle(ctx, g)
}
