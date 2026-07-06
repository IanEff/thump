package hiss

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
)

// HandleForTest exposes Transport.handle to hiss_test without handle
// becoming part of hiss's real API. Only compiled under `go test` — the
// _test.go suffix keeps it out of the shipped binary. Mirrors
// internal/clank/export_test.go and internal/rattle/export_test.go.
func (tr *Transport) HandleForTest(ctx context.Context, ps proposal.Set) error {
	return tr.handle(ctx, ps)
}
