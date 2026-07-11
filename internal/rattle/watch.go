// Package rattle: LoadWatch reads the per-site watch list rattle polls,
// replacing the compiled-in loadSLOs() literal (C2) — the last
// site-specific fact that used to live in the binary.
package rattle

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// LoadWatch reads path and parses it into the SLOs rattle polls. A watch
// list with zero SLOs is a misconfiguration — fail loud rather than run a
// silent empty poll.
func LoadWatch(path string) ([]SLO, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed config path, not user input
	if err != nil {
		return nil, fmt.Errorf("read watch file %s: %w", path, err)
	}

	var file struct {
		SLOs []struct {
			ID           string  `json:"id"`
			Object       string  `json:"object"`
			Tier         string  `json:"tier"`
			Objective    float64 `json:"objective"`
			ContractRef  string  `json:"contractRef"`
			Dependencies []struct {
				Name string `json:"name"`
				Role string `json:"role"`
			} `json:"dependencies"`
		} `json:"slos"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse watch file: %w", err)
	}

	if len(file.SLOs) == 0 {
		return nil, fmt.Errorf("watch file %s declares zero SLOs", path)
	}

	out := make([]SLO, len(file.SLOs))
	for i, s := range file.SLOs {
		deps := make([]Dependency, len(s.Dependencies))
		for j, d := range s.Dependencies {
			deps[j] = Dependency{Name: d.Name, Role: d.Role}
		}
		out[i] = SLO{
			ID:           s.ID,
			Object:       s.Object,
			Tier:         s.Tier,
			Objective:    s.Objective,
			ContractRef:  s.ContractRef,
			Dependencies: deps,
		}
	}
	return out, nil
}
