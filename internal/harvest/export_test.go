package harvest

import (
	"context"
	"io"
)

// RunForTest exposes run to harvest_test — the scenario-loop wiring Main
// calls after building its signal-aware ctx, independent of flag parsing or
// the NATS connection.
func RunForTest(ctx context.Context, h *Harvest, table Table, only string, asJSON bool, stdout, stderr io.Writer) int {
	return run(ctx, h, table, only, asJSON, stdout, stderr)
}
