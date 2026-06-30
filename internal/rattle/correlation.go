package rattle

type MultiSignalWindow map[string][]Sample

type CorrelationDetector struct {
	MinSignals int
}

func (d CorrelationDetector) Fires(w MultiSignalWindow) bool {
	worsening := 0
	for _, series := range w {
		if mean(diffs(burnRates(series))) > 0 {
			worsening++
		}
	}
	return worsening >= d.MinSignals
}
