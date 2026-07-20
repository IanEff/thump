package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
	"sigs.k8s.io/yaml"
)

// EvidenceQuery represents a single named query from the evidence-queries.yaml file.
type EvidenceQuery struct {
	Name    string `json:"name"`
	Query   string `json:"query"`
	Subject string `json:"subject,omitempty"` // the whir catalog-info.yaml entity this query's result is about; omitting it makes no topology claim — see EvidenceRef.Subject
}

// MetricsTool is the production implementation of the "metrics" tool.
// It executes read-only PromQL queries against a Prometheus API.
type MetricsTool struct {
	BaseURL string
	Client  *http.Client
	Queries map[string]string
	// Subjects maps a query name to the topology node it concerns, for the
	// gate's cross-domain coherence check (EvidenceRef.Subject). A name
	// absent from this map stamps no Subject — the query makes no
	// topology claim, so its Live citation is never attenuated.
	Subjects map[string]string
}

// Ensure MetricsTool implements the Tool interface.
var _ Tool = (*MetricsTool)(nil)

type metricsInput struct {
	Q string `json:"q"`
}

// Spec returns the schema so the model knows how to call this tool. The
// valid `q` names are only known at runtime (loaded per-cluster from
// evidence-queries.yaml, ceph-lab and rook-gke declare different sets), so
// they're listed in the description here rather than a static schema enum —
// without this the model can only discover valid names by guessing and
// getting back "no such evidence query", which reads indistinguishably from
// "no metrics are accessible" (confirmed live 2026-07-08: the model declined
// a real detection citing no accessible Ceph/OSD/recovery data while every
// one of those queries was returning live, non-empty results).
func (m *MetricsTool) Spec() ToolSpec {
	names := make([]string, 0, len(m.Queries))
	for name := range m.Queries {
		names = append(names, name)
	}
	sort.Strings(names)
	return ToolSpec{
		Name:        "metrics",
		Description: "read-only telemetry query. Valid q values: " + strings.Join(names, ", "),
		InputSchema: SchemaOf[metricsInput](),
	}
}

// Run executes the query.  It returns Live:true only if it gets a fresh, non-error, non-empty result.
func (m *MetricsTool) Run(ctx context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	var input metricsInput
	if err := json.Unmarshal(args, &input); err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("decode args: %w", err)
	}

	promQL, ok := m.Queries[input.Q]
	if !ok {
		return proposal.EvidenceRef{
			Tool:    "metrics",
			Query:   input.Q,
			Summary: fmt.Sprintf("no such evidence query: %s", input.Q),
			Live:    false,
		}, nil
	}
	subject := m.Subjects[input.Q]

	u, err := url.Parse(m.BaseURL + "/api/v1/query")
	if err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("parse url: %w", err)
	}
	u.RawQuery = url.Values{"query": {promQL}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("new request: %w", err)
	}

	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return proposal.EvidenceRef{
			Tool:    "metrics",
			Query:   input.Q,
			Summary: fmt.Sprintf("prometheus request failed: %v", err),
			Live:    false,
			Subject: subject,
		}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return proposal.EvidenceRef{
			Tool:    "metrics",
			Query:   input.Q,
			Summary: fmt.Sprintf("prometheus returned status: %s", resp.Status),
			Live:    false,
			Subject: subject,
		}, nil
	}

	var body struct {
		Data struct {
			Result []struct {
				Value [2]json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return proposal.EvidenceRef{}, nil
	}

	if len(body.Data.Result) == 0 {
		return proposal.EvidenceRef{
			Tool:    "metrics",
			Query:   input.Q,
			Summary: "query returned no data",
			Live:    false,
			Subject: subject,
		}, nil
	}

	var v string
	if err := json.Unmarshal(body.Data.Result[0].Value[1], &v); err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("decode value string: %w", err)
	}

	return proposal.EvidenceRef{
		Tool:    "metrics",
		Query:   input.Q,
		Summary: fmt.Sprintf("%s = %s", input.Q, v),
		Ref:     "metrics://" + input.Q,
		Live:    true,
		Subject: subject,
	}, nil
}

// LoadEvidenceQueries parses evidence-queries.yaml into a query lookup and a
// parallel subjects lookup — a name absent from subjects declared no
// subject: tag, so MetricsTool stamps no Subject for it (see
// EvidenceRef.Subject).
func LoadEvidenceQueries(path string) (queries map[string]string, subjects map[string]string, err error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, nil, fmt.Errorf("read evidence queries file %s: %w", path, err)
	}

	var file struct {
		Queries []EvidenceQuery `json:"queries"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, nil, fmt.Errorf("parse evidence queries: %w", err)
	}

	queries = make(map[string]string, len(file.Queries))
	subjects = make(map[string]string, len(file.Queries))
	for _, q := range file.Queries {
		queries[q.Name] = q.Query
		if q.Subject != "" {
			subjects[q.Name] = q.Subject
		}
	}
	return queries, subjects, nil
}
