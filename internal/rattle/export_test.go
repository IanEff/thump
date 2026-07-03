package rattle

// LoadSLOsForTest exposes the compiled-in watch list to rattle_test without
// loadSLOs becoming part of rattle's real API. Only compiled under `go test`
// — the _test.go suffix keeps it out of the shipped binary. Mirrors
// internal/clank/export_test.go.
func LoadSLOsForTest() []SLO { return loadSLOs() }
