package thump

import "context"

// ConvergenceProbe is the primitive seam a real Converger is injected
// through — a metric name and a target string.
type ConvergenceProbe interface {
	Converged(ctx context.Context, metric, target string) bool
	Severity(ctx context.Context, query string) (value float64, ok bool)
}

// PrometheusConverger adapts a primitive ConvergenceProbe into
// thump's Converger by unpacking the Order fields each read needs.
type PrometheusConverger struct {
	Probe ConvergenceProbe
}

// Settle reads o's convergence once: the reversal verdict from its success
// metric, and — when o authored a SeverityQuery — the normalized post-action
// severity.
func (p PrometheusConverger) Settle(ctx context.Context, o Order) (converged bool, severity *float64) {
	converged = p.Probe.Converged(ctx, o.Success.Metric, o.Success.Target)
	if o.Success.SeverityQuery != "" {
		if v, ok := p.Probe.Severity(ctx, o.Success.SeverityQuery); ok {
			severity = &v
		}
	}
	return converged, severity
}
