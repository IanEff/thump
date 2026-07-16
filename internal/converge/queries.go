package converge

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

type query struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

// LoadQueries parses path -- an evidence-queries.yaml file-- into a
// metric-name -> promql lookup map.
func LoadQueries(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path) // nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("converge: read queries file %s: %w", path, err)
	}
	var file struct {
		Queries []query `json:"queries"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("converge: parse queries: %w", err)
	}
	out := make(map[string]string, len(file.Queries))
	for _, q := range file.Queries {
		out[q.Name] = q.Query
	}
	return out, nil
}
