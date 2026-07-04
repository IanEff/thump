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

type StateQuery struct {
	Dependency string `json:"dependency"`
	Query      string `json:"query"`
}

type Resolver struct {
	BaseURL string
	Client  *http.Client
	Queries map[string]string
}

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
