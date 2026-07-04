package rattle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"sigs.k8s.io/yaml"
)

type TrafficQueries struct {
	Affected string
	Total    string
}

func LoadTrafficQueries(path string) (map[string]TrafficQueries, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // fixed config path, not user input
	if err != nil {
		return nil, fmt.Errorf("read traffic queries file %s: %w", path, err)
	}
	var file struct {
		SLOs []struct {
			ID       string `json:"id"`
			Affected string `json:"affected"`
			Total    string `json:"total"`
		} `json:"slos"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse traffic queries: %w", err)
	}
	out := make(map[string]TrafficQueries, len(file.SLOs))
	for _, s := range file.SLOs {
		out[s.ID] = TrafficQueries{Affected: s.Affected, Total: s.Total}
	}
	return out, nil
}

type HubbleTrafficSource struct {
	BaseURL string
	Client  *http.Client
	Queries map[string]TrafficQueries
}

func (h *HubbleTrafficSource) TrafficSamples(ctx context.Context, slo SLO) ([]TrafficSample, error) {
	q, ok := h.Queries[slo.ID]
	if !ok {
		return nil, nil // no entry for this SLO — quiet absence, not a fault
	}

	affected, err := h.instant(ctx, q.Affected)
	if err != nil {
		return nil, fmt.Errorf("hubble affected query for %s: %w", slo.ID, err)
	}
	total, err := h.instant(ctx, q.Total)
	if err != nil {
		return nil, fmt.Errorf("hubble total query for %s: %w", slo.ID, err)
	}
	if affected == nil || total == nil {
		return nil, nil // nothing measured — quiet, matches BurnSamples' empty window
	}

	pct := 0.0
	if *total > 0 {
		pct = min(*affected/(*total), 1.0) // clamp: affected > total ⇒ 1.0
	}
	return []TrafficSample{{T: time.Now(), AffectedPct: pct}}, nil
}

// instant runs one /api/v1/query. A nil *float64 with nil error means "no
// series" (quiet); a non-nil error means the query itself failed (loud).
func (h *HubbleTrafficSource) instant(ctx context.Context, query string) (*float64, error) {
	u, err := url.Parse(h.BaseURL + "/api/v1/query")
	if err != nil {
		return nil, fmt.Errorf("hubble base url: %w", err)
	}
	u.RawQuery = url.Values{"query": {query}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build hubble request: %w", err)
	}
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query hubble: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hubble returned %s", resp.Status)
	}

	var body struct {
		Data struct {
			Result []struct {
				Value [2]json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode hubble body: %w", err)
	}
	if len(body.Data.Result) == 0 {
		return nil, nil // no series — quiet absence
	}
	var raw string
	if err := json.Unmarshal(body.Data.Result[0].Value[1], &raw); err != nil {
		return nil, fmt.Errorf("parse hubble value: %w", err)
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("hubble value %q not a float: %w", raw, err)
	}
	return &f, nil
}
