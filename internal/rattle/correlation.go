package rattle

// MultiSignalWindow is one burn-rate series per correlated metric, keyed by
// metric name — CorrelationDetector's input.
type MultiSignalWindow map[string][]Sample

// CorrelationDetector fires when at least MinSignals independent metrics are
// worsening at once — corroboration across signals standing in for
// acceleration on any single one.
type CorrelationDetector struct {
	MinSignals int
}

// Fires reports whether MinSignals or more series in w have a positive mean
// first-difference (a worsening trend), regardless of how much any one
// series has moved.
func (d CorrelationDetector) Fires(w MultiSignalWindow) bool {
	worsening := 0
	for _, series := range w {
		if mean(diffs(burnRates(series))) > 0 {
			worsening++
		}
	}
	return worsening >= d.MinSignals
}
