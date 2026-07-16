package thump

import "context"

// ConvergenceProbe is the primitive seam a real Converger is injected
// through-- a metric name and a target string.
type ConvergenceProbe interface {
	Converged(ctx context.Context, metric, target string) bool
}

// PrometheusConverger adapts a primitive ConvergenceProbe into
// thump's Converget by unpacking the one Order field the probe
// actually needs.
type PrometheusConverger struct {
	Probe ConvergenceProbe
}

func (p PrometheusConverger) Converged(ctx context.Context, o Order) bool {
	return p.Probe.Converged(ctx, o.Success.Metric, o.Success.Target)
}
