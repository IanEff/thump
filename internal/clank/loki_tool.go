package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
)

type lokiInput struct {
	Namespace string            `json:"namespace" jsonschema:"required"`
	Labels    map[string]string `json:"labels,omitempty"`
	Query     string            `json:"query,omitempty"` // substring line-filter, NOT raw LogQL
	Limit     int               `json:"limit,omitempty"`
	// Lookback is how far back from now to search, e.g. "15m", "1h". Defaults to 15m.
	Lookback string `json:"lookback,omitempty"`
}

// LokiTool is the production implementation of the "loki" tool. It executes
// read-only log queries against a Loki query_range API.
type LokiTool struct {
	BaseURL string
	Client  *http.Client
}

var _ Tool = (*LokiTool)(nil)

func (l *LokiTool) Spec() ToolSpec {
	return ToolSpec{
		Name: "loki",
		Description: "read-only log query. namespace is required. Known label keys: " +
			"app, ceph_daemon_id, ceph_daemon_type, component, container, instance, job, " +
			"node_name, pod, service_name. query is an optional line-filter substring " +
			"(NOT LogQL syntax) — do not pass raw LogQL, it will be escaped as a literal.",
		InputSchema: SchemaOf[lokiInput](),
	}
}

// Run executes the query_range request. It returns Live:true only if the
// response contains at least one log line.
func (l *LokiTool) Run(ctx context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	var input lokiInput
	if err := json.Unmarshal(args, &input); err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("decode loki args: %w", err)
	}

	logQL := buildLogQL(input.Namespace, input.Labels, input.Query)

	lookback := input.Lookback
	if lookback == "" {
		lookback = "15m"
	}
	lb, err := time.ParseDuration(lookback)
	if err != nil {
		return proposal.EvidenceRef{
			Tool:    "loki",
			Query:   logQL,
			Summary: fmt.Sprintf("invalid lookback: %v", err),
			Live:    false,
		}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}

	end := time.Now()
	start := end.Add(-lb)

	u, err := url.Parse(l.BaseURL + "/loki/api/v1/query_range")
	if err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("parse url: %w", err)
	}
	u.RawQuery = url.Values{
		"query": {logQL},
		"start": {strconv.FormatInt(start.UnixNano(), 10)},
		"end":   {strconv.FormatInt(end.UnixNano(), 10)},
		"limit": {strconv.Itoa(limit)},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("new request: %w", err)
	}

	client := l.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return proposal.EvidenceRef{
			Tool:    "loki",
			Query:   logQL,
			Summary: fmt.Sprintf("loki request failed: %v", err),
			Live:    false,
		}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return proposal.EvidenceRef{
			Tool:    "loki",
			Query:   logQL,
			Summary: fmt.Sprintf("loki returned status: %s", resp.Status),
			Live:    false,
		}, nil
	}

	var body struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return proposal.EvidenceRef{
			Tool:    "loki",
			Query:   logQL,
			Summary: fmt.Sprintf("decode loki response: %v", err),
			Live:    false,
		}, nil
	}

	total := 0
	var lastLine string
	for _, stream := range body.Data.Result {
		total += len(stream.Values)
		if n := len(stream.Values); n > 0 {
			lastLine = stream.Values[n-1][1]
		}
	}

	if total == 0 {
		return proposal.EvidenceRef{
			Tool:    "loki",
			Query:   logQL,
			Summary: "no matching log lines",
			Live:    false,
		}, nil
	}

	summary := fmt.Sprintf("%d log line(s)", total)
	if lastLine != "" {
		summary += "; last: " + truncateLine(lastLine, 200)
	}

	return proposal.EvidenceRef{
		Tool:    "loki",
		Query:   logQL,
		Summary: summary,
		Ref:     lokiRef(input.Namespace, input.Labels),
		Live:    true,
	}, nil
}

// buildLogQL assembles a LogQL stream selector server-side from structured
// input, sorting label keys for determinism, and appends a quoted line-filter
// if query is non-empty. Values are always run through strconv.Quote — the
// model's query/label input is never spliced into the LogQL string raw.
func buildLogQL(namespace string, labels map[string]string, query string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	matchers := make([]string, 0, len(keys)+1)
	matchers = append(matchers, "namespace="+strconv.Quote(namespace))
	for _, k := range keys {
		matchers = append(matchers, k+"="+strconv.Quote(labels[k]))
	}

	logQL := "{" + strings.Join(matchers, ", ") + "}"
	if query != "" {
		logQL += " |= " + strconv.Quote(query)
	}
	return logQL
}

// lokiRef renders a stable evidence ref for a namespace + label selector.
func lokiRef(namespace string, labels map[string]string) string {
	if len(labels) == 0 {
		return "loki://" + namespace
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + labels[k]
	}
	return "loki://" + namespace + "/" + strings.Join(parts, ",")
}

func truncateLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
