package whir

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	StateHealthy  = "healthy"
	StateDegraded = "degraded"
	StateUnknown  = "unknown"
)

// Entity is the slice of Backstage's catalog-info.yaml that's actually pertinent: metadata.name + spec.dependsOn.
type Entity struct {
	Name      string
	DependsOn []string // bare names -- refs are stripped at parse time
}

type Catalog struct {
	Entities []Entity
}

func (c Catalog) Edges(name string) []string {
	for _, e := range c.Entities {
		if e.Name == name {
			return e.DependsOn
		}
	}
	return nil
}

func Load(raw []byte) (Catalog, error) {
	var cat Catalog

	for _, doc := range bytes.Split(raw, []byte("\n---")) {
		var e struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				DependsOn []string `json:"dependsOn"`
			} `json:"spec"`
		}

		if err := yaml.Unmarshal(doc, &e); err != nil {
			return Catalog{}, fmt.Errorf("parse catalog doc: %w", err)
		}

		if e.Metadata.Name == "" {
			continue // comment-only doc
		}

		deps := make([]string, len(e.Spec.DependsOn))
		for i, ref := range e.Spec.DependsOn {
			deps[i] = ref[strings.LastIndex(ref, "/")+1:]
		}

		cat.Entities = append(cat.Entities, Entity{Name: e.Metadata.Name, DependsOn: deps})
	}

	return cat, nil
}

func LoadCatalogFile(path string) (Catalog, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return Catalog{}, fmt.Errorf("read catalog file %s: %w", path, err)
	}
	return Load(raw)
}
