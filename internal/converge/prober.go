// Package converge answers thump's one post-action question — "did this
// work?" — by reading Prometheus directly, the same instant-query shape
// internal/whir/resolve.go already uses to read topology health. It lives
// outside package thump because thump's dry-run allowlist
// (structural_test.go) forbids net/http there: Prober is expressed in
// primitives (a metric name, a target string), never thump.Order, so package
// thump can wrap it in its own adapter — the same Live/ActionRunner split —
// without a cyclic import.
package converge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// Prober is thump's real Converger. BaseURL and Client behave exactly like
// whir.Resolver's — Client defaults to http.DefaultClient when nil — and
// Queries maps a SuccessCriteria.Metric name to the PromQL instant-query
// expression that reports its live value.
type Prober struct {
	BaseURL string
	Client  *http.Client
	Queries map[string]string
}

// Converged asks whether metric's current live value satisfies target's
// prose comparison. Every way the answer could be untrustworthy — metric has
// no bound query, target doesn't parse, the request fails, the response
// isn't 200, the result vector is empty — resolves to false, never true:
// a false "not converged" only costs firing a reversal against an
// already-healthy system, but a false "converged" strands an unresolved
// incident with its undo skipped. Fail closed, not open.
func (p *Prober) Converged(ctx context.Context, metric, target string) bool {
	op, threshold, err := parseTarget(target)
	if err != nil {
		return false
	}
	query, ok := p.Queries[metric]
	if !ok {
		return false
	}
	value, ok := p.query(ctx, query)
	if !ok {
		return false
	}
	return compare(op, value, threshold)
}

// Severity resolves the query's name through Queries and reports its live, normalized value.
func (p *Prober) Severity(ctx context.Context, query string) (value float64, ok bool) {
	promql, ok := p.Queries[query]
	if !ok {
		return 0, false
	}
	return p.query(ctx, promql)
}

func (p *Prober) query(ctx context.Context, query string) (float64, bool) {
	u, err := url.Parse(p.BaseURL + "/api/v1/query")
	if err != nil {
		return 0, false
	}
	u.RawQuery = url.Values{"query": {query}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, false
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}

	var body struct {
		Data struct {
			Result []struct {
				Value [2]json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, false
	}
	if len(body.Data.Result) == 0 {
		return 0, false
	}

	var v string
	if err := json.Unmarshal(body.Data.Result[0].Value[1], &v); err != nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func compare(op string, value, threshold float64) bool {
	switch op {
	case "<":
		return value < threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}
