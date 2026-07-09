package whir

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"sigs.k8s.io/yaml"
)

// StateQuery binds one dependency name to the Prometheus-style query that
// reports its health — the on-disk shape LoadStateQueries parses.
type StateQuery struct {
	Dependency string `json:"dependency"`
	Query      string `json:"query"`
}

// Resolver answers State by running each dependency's configured query
// against a Prometheus-shaped HTTP API. A dependency with no entry in
// Queries, or any failure along the way (network, non-200, unparseable
// body), resolves to StateUnknown rather than an error — a caller reading
// topology health has no error path to handle, only three states.
type Resolver struct {
	BaseURL string
	// Client is the HTTP client State issues queries with. Defaults to
	// http.DefaultClient when nil.
	Client *http.Client
	// Queries maps a dependency name to its instant-query expression, as
	// loaded by LoadStateQueries.
	Queries map[string]string
}

// State runs dependency's configured query against BaseURL's instant-query
// endpoint and classifies the first result: a positive value is
// StateHealthy, non-positive is StateDegraded. Any failure — no configured
// query, a request or transport error, a non-200 response, or a body that
// doesn't parse — returns StateUnknown.
func (r *Resolver) State(ctx context.Context, dependency string) string {
	query, ok := r.Queries[dependency]
	if !ok {
		return StateUnknown
	}

	u, err := url.Parse(r.BaseURL + "/api/v1/query")
	if err != nil {
		return StateUnknown
	}
	u.RawQuery = url.Values{"query": {query}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return StateUnknown
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return StateUnknown
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return StateUnknown
	}

	var body struct {
		Data struct {
			Result []struct {
				Value [2]json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return StateUnknown
	}
	if len(body.Data.Result) == 0 {
		return StateUnknown
	}

	var v string
	if err := json.Unmarshal(body.Data.Result[0].Value[1], &v); err != nil {
		return StateUnknown
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return StateUnknown
	}
	if f > 0 {
		return StateHealthy
	}
	return StateDegraded
}

// LoadStateQueries reads path — a YAML file of StateQuery entries — and
// returns them as a dependency-name-to-query map, ready to assign to
// Resolver.Queries.
func LoadStateQueries(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read state queries file %s: %w", path, err)
	}

	var file struct {
		Queries []StateQuery `json:"queries"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse state queries: %w", err)
	}

	out := make(map[string]string, len(file.Queries))
	for _, q := range file.Queries {
		out[q.Dependency] = q.Query
	}
	return out, nil
}
