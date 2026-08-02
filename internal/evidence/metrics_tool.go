package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/schema"
	"github.com/ianeff/thump/internal/subjects"
	"sigs.k8s.io/yaml"
)

// Query represents a single named query from the evidence-queries.yaml file.
type Query struct {
	Name    string `json:"name"`
	Query   string `json:"query"`
	Subject string `json:"subject,omitempty"` // the whir catalog-info.yaml entity this query's result is about; omitting it makes no topology claim — see EvidenceRef.Subject
}

// MetricsTool is the production implementation of the "metrics" tool.
// It executes read-only PromQL queries against a Prometheus API.
type MetricsTool struct {
	BaseURL string
	Client  *http.Client
	// Queries maps a query name to the PromQL and topology node (Subject) it
	// concerns, for the gate's cross-domain coherence check
	// (EvidenceRef.Subject). A zero-value Subject stamps no Subject — the
	// query makes no topology claim, so its Live citation is never
	// attenuated.
	Queries map[string]Query
}

// Ensure MetricsTool implements the reason.Tool interface.
var _ reason.Tool = (*MetricsTool)(nil)

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
func (m *MetricsTool) Spec() reason.ToolSpec {
	names := make([]string, 0, len(m.Queries))
	for name := range m.Queries {
		names = append(names, name)
	}
	sort.Strings(names)
	return reason.ToolSpec{
		Name:        "metrics",
		Description: "read-only telemetry query. Valid q values: " + strings.Join(names, ", "),
		InputSchema: schema.Of[metricsInput](),
	}
}

// Run executes the query.  It returns Live:true only if it gets a fresh, non-error, non-empty result.
func (m *MetricsTool) Run(ctx context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	var input metricsInput
	if err := json.Unmarshal(args, &input); err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("decode args: %w", err)
	}

	eq, ok := m.Queries[input.Q]
	if !ok {
		return proposal.EvidenceRef{
			Tool:    "metrics",
			Query:   input.Q,
			Summary: fmt.Sprintf("no such evidence query: %s", input.Q),
			Live:    false,
		}, nil
	}
	promQL, subject := eq.Query, eq.Subject

	result, err := httpx.InstantQuery(ctx, m.Client, m.BaseURL, promQL)
	if err != nil {
		switch {
		case errors.Is(err, httpx.ErrBuildInstantQuery):
			return proposal.EvidenceRef{}, fmt.Errorf("build query: %w", err)
		case errors.Is(err, httpx.ErrDecodeInstantResult):
			return proposal.EvidenceRef{}, nil
		default:
			if statusErr, ok := errors.AsType[*httpx.StatusError](err); ok {
				return proposal.EvidenceRef{
					Tool:    "metrics",
					Query:   input.Q,
					Summary: fmt.Sprintf("prometheus returned status: %s", statusErr.Status),
					Live:    false,
					Subject: subject,
				}, nil
			}
			return proposal.EvidenceRef{
				Tool:    "metrics",
				Query:   input.Q,
				Summary: fmt.Sprintf("prometheus request failed: %v", err),
				Live:    false,
				Subject: subject,
			}, nil
		}
	}

	if len(result.Data.Result) == 0 {
		return proposal.EvidenceRef{
			Tool:    "metrics",
			Query:   input.Q,
			Summary: "query returned no data",
			Live:    false,
			Subject: subject,
		}, nil
	}

	var v string
	if err := json.Unmarshal(result.Data.Result[0].Value[1], &v); err != nil {
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

// Config is one rig's evidence surface: the named PromQL the metrics
// tool exposes, the topology node each of those queries is about, and the
// coordinate rules that tell the log and cluster tools the same thing. All
// three answer one question — which node is this evidence about? — so they
// are authored in one file per rig.
type Config struct {
	Queries map[string]Query      // query name → Query
	Index   subjects.SubjectIndex // the log and cluster tools' coordinate rules
}

// LoadEvidenceConfig parses evidence-queries.yaml. A query with no subject:
// tag stores an Query with an empty Subject, so MetricsTool stamps
// none for it via the zero value (see EvidenceRef.Subject).
func LoadEvidenceConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config file path, not user input
	if err != nil {
		return Config{}, fmt.Errorf("read evidence queries file %s: %w", path, err)
	}

	var file struct {
		Queries  []Query                `json:"queries"`
		Subjects []subjects.SubjectRule `json:"subjects"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return Config{}, fmt.Errorf("parse evidence queries: %w", err)
	}

	cfg := Config{
		Queries: make(map[string]Query, len(file.Queries)),
		Index:   file.Subjects,
	}
	for _, q := range file.Queries {
		cfg.Queries[q.Name] = q
	}
	return cfg, nil
}
