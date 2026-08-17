package whir

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/ianeff/thump/internal/httpx"
	"sigs.k8s.io/yaml"
)

// Edge is one dependency the service graph observed — carrying the share
// of the client's outbound requests it handled and the share of those that failed.
type Edge struct {
	Client   string  // the calling service
	Server   string  // the upstream dependency
	Share    float64 // fraction of the client's total outbound requests — sum of shares across outbound edges is 1.0
	FailRate float64 // failed requests ÷ total requests on this edge
	State    string  // StateHealthy or StateDegraded based on FailedThreshold
}

// GraphQueries carries the authored PromQL query templates for GraphSource —
// queries are authored in YAML rather than compiled in to keep the signal contract explicit.
type GraphQueries struct {
	Requests        string  `json:"requests" yaml:"requests"`                                   // template for client outbound requests: sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))
	Failed          string  `json:"failed" yaml:"failed"`                                       // template for client outbound failures: sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))
	InboundRequests string  `json:"inboundRequests,omitempty" yaml:"inboundRequests,omitempty"` // template for inbound caller requests: sum by (client, server) (rate(traces_service_graph_request_total{server="%s"}[%s]))
	InboundFailed   string  `json:"inboundFailed,omitempty" yaml:"inboundFailed,omitempty"`     // template for inbound caller failures: sum by (client, server) (rate(traces_service_graph_request_failed_total{server="%s"}[%s]))
	FailedThreshold float64 `json:"failedThreshold,omitempty" yaml:"failedThreshold,omitempty"` // error-rate floor above which an edge is classified as StateDegraded; defaults to 0.05
}

// GraphSource reports the dependency graph as observed rather than as
// authored: one Edge per client/server pair the service-graph connector
// emitted inside Window. It answers the same question Catalog.Edges does and
// does not replace it — a service that emits no traces has no observed edges
// and no error, the same way Resolver.State collapses "couldn't tell" into a
// state rather than a failure.
type GraphSource struct {
	BaseURL string
	// Client is the HTTP client queries are issued with — defaults to httpx.DefaultBackendTimeout when nil.
	Client *http.Client
	// Window is the rate lookback duration — defaults to 5m when <= 0.
	Window time.Duration
	// Queries carries the authored PromQL templates.
	Queries GraphQueries
}

// Edges returns the observed outbound dependencies for the named client service —
// returns nil, nil when no traces were emitted for the service.
func (g *GraphSource) Edges(ctx context.Context, name string) ([]Edge, error) {
	return g.queryEdges(ctx, g.Queries.Requests, g.Queries.Failed, name, "server",
		func(other string) Edge { return Edge{Client: name, Server: other} })
}

// Inbound returns the observed callers calling the named server service —
// returns nil, nil when no inbound traces were observed.
func (g *GraphSource) Inbound(ctx context.Context, name string) ([]Edge, error) {
	return g.queryEdges(ctx, g.Queries.InboundRequests, g.Queries.InboundFailed, name, "client",
		func(other string) Edge { return Edge{Client: other, Server: name} })
}

// queryEdges runs reqTemplate/failTemplate against name and derives one Edge
// per distinct value of otherLabel — the metric label naming the other side
// of the edge ("server" for Edges, "client" for Inbound) — via newEdge, which
// supplies the Client/Server pair. Returns nil, nil when reqTemplate is unset
// or the request query returns no series, the same "couldn't tell" collapse
// Resolver.State uses.
func (g *GraphSource) queryEdges(ctx context.Context, reqTemplate, failTemplate, name, otherLabel string, newEdge func(other string) Edge) ([]Edge, error) {
	if reqTemplate == "" {
		return nil, nil
	}

	window := g.Window
	if window <= 0 {
		window = 5 * time.Minute
	}
	windowStr := window.String()

	reqQuery := fmt.Sprintf(reqTemplate, name, windowStr)
	reqResult, err := httpx.InstantQuery(ctx, g.Client, g.BaseURL, reqQuery)
	if err != nil || len(reqResult.Data.Result) == 0 {
		return nil, nil
	}

	reqMap := make(map[string]float64)
	totalReq := 0.0
	var others []string

	for _, res := range reqResult.Data.Result {
		other := res.Metric[otherLabel]
		if other == "" {
			continue
		}
		val := parseRawFloat(res.Value[1])
		if val <= 0 {
			continue
		}
		reqMap[other] = val
		totalReq += val
		others = append(others, other)
	}

	if totalReq <= 0 {
		return nil, nil
	}

	failMap := make(map[string]float64)
	if failTemplate != "" {
		failQuery := fmt.Sprintf(failTemplate, name, windowStr)
		if failResult, err := httpx.InstantQuery(ctx, g.Client, g.BaseURL, failQuery); err == nil {
			for _, res := range failResult.Data.Result {
				other := res.Metric[otherLabel]
				if other == "" {
					continue
				}
				failMap[other] = parseRawFloat(res.Value[1])
			}
		}
	}

	threshold := g.Queries.FailedThreshold
	if threshold <= 0 {
		threshold = 0.05
	}

	slices.Sort(others)
	edges := make([]Edge, 0, len(others))
	for _, other := range others {
		req := reqMap[other]
		fail := failMap[other]
		failRate := 0.0
		if req > 0 {
			failRate = fail / req
		}
		state := StateHealthy
		if failRate >= threshold {
			state = StateDegraded
		}
		edge := newEdge(other)
		edge.Share = req / totalReq
		edge.FailRate = failRate
		edge.State = state
		edges = append(edges, edge)
	}

	return edges, nil
}

// LoadGraphQueries reads path — a YAML file of GraphQueries — and returns it.
func LoadGraphQueries(path string) (GraphQueries, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config file path, not user input
	if err != nil {
		return GraphQueries{}, fmt.Errorf("read graph queries file %s: %w", path, err)
	}

	var file struct {
		Version string       `json:"version"`
		Queries GraphQueries `json:"queries"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return GraphQueries{}, fmt.Errorf("parse graph queries %s: %w", path, err)
	}

	if file.Queries.Requests != "" || file.Queries.Failed != "" {
		return file.Queries, nil
	}

	var direct GraphQueries
	if err := yaml.Unmarshal(raw, &direct); err != nil {
		return GraphQueries{}, fmt.Errorf("parse graph queries %s: %w", path, err)
	}
	return direct, nil
}

func parseRawFloat(raw json.RawMessage) float64 {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	return 0
}
