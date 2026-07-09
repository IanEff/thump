// Package whir resolves topology: a static dependency graph (Backstage's
// catalog-info.yaml, parsed by Load) plus, optionally, a live health state
// per dependency (Resolver, backed by a Prometheus-shaped query API).
// rattle and thump read it to know what's downstream of what — whir has no
// opinion on what to do with that answer, only what it is. It is a leaf
// package (leaf_test.go): stdlib HTTP/JSON plus sigs.k8s.io/yaml only, no
// beat internals.
package whir

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// The three states State (Resolver.State) ever returns. StateUnknown is
// the answer for anything that isn't a confirmed StateHealthy or
// StateDegraded — a missing query, a failed request, or an unparseable
// response all collapse to it rather than a caller-visible error, because
// "we couldn't tell" and "not affected" must never look alike.
const (
	StateHealthy  = "healthy"
	StateDegraded = "degraded"
	StateUnknown  = "unknown"
)

// Entity is the slice of Backstage's catalog-info.yaml that's actually
// pertinent: metadata.name + spec.dependsOn. Everything else in the
// document is discarded at parse time.
type Entity struct {
	Name      string
	DependsOn []string // bare names -- refs are stripped at parse time
}

// Catalog is the static dependency graph Load parses out of one or more
// catalog-info.yaml documents.
type Catalog struct {
	Entities []Entity
}

// Edges returns the dependencies declared for the entity named name, or
// nil if no entity by that name was loaded.
func (c Catalog) Edges(name string) []string {
	for _, e := range c.Entities {
		if e.Name == name {
			return e.DependsOn
		}
	}
	return nil
}

// Load parses raw as one or more YAML documents separated by "\n---",
// keeping only each document's metadata.name and spec.dependsOn (a ref
// like "component/foo" is reduced to its bare name "foo"). A document with
// no metadata.name is skipped as comment-only, not an error.
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

// LoadCatalogFile reads path and parses it with Load.
func LoadCatalogFile(path string) (Catalog, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return Catalog{}, fmt.Errorf("read catalog file %s: %w", path, err)
	}
	return Load(raw)
}
