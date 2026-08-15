package harvest

import (
	"context"
	"io"
	"time"
)

// RunForTest exposes run to harvest_test — the scenario-loop wiring Main
// calls after building its signal-aware ctx, independent of flag parsing or
// the NATS connection.
func RunForTest(ctx context.Context, h *Harvest, table Table, only string, asJSON bool, repeat int, cooldown time.Duration, stdout, stderr io.Writer) int {
	return run(ctx, h, table, only, asJSON, repeat, cooldown, stdout, stderr)
}

// VerifyKubeContextForTest exposes verifyKubeContext to harvest_test with a
// stub context checker, so the cluster guard is provable without a real
// kubeconfig.
func VerifyKubeContextForTest(ctx context.Context, want string, check func(context.Context) (string, error)) error {
	return verifyKubeContext(ctx, want, check)
}
