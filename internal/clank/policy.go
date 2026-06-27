package clank

type GatePolicy struct {
	Threshold     map[string]map[FailureClass]float64
	CausalWeights CausalWeights
}

type CausalWeights struct {
	Temporal    float64
	Topological float64
	Historical  float64
}
