package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/schema"
	"github.com/ianeff/thump/internal/subjects"
)

var traceKeyRegex = regexp.MustCompile(`^(resource\.|span\.)?[a-zA-Z_][a-zA-Z0-9_.]*$`)

type tracesInput struct {
	Namespace  string            `json:"namespace" jsonschema:"required"`
	Labels     map[string]string `json:"labels,omitempty"`
	ErrorsOnly bool              `json:"errorsOnly,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	Lookback   string            `json:"lookback,omitempty"`
}

// TracesTool is the production implementation of the "traces" tool — querying
// Tempo search API for distributed trace spans using server-sanitized TraceQL.
type TracesTool struct {
	BaseURL  string
	Client   *http.Client
	Subjects subjects.SubjectIndex
}

var _ reason.Tool = (*TracesTool)(nil)

// Spec advertises the traces tool and its authored selectors on the traces plane.
func (t *TracesTool) Spec() reason.ToolSpec {
	desc := "read-only distributed-trace search. namespace is required."
	if sels := t.Subjects.Selectors("traces"); len(sels) > 0 {
		desc += " Authored trace selectors: " + strings.Join(sels, "; ") + "."
	}
	desc += " errorsOnly restricts the search to spans with an error status." +
		" labels are attribute matchers (NOT TraceQL syntax) - do not pass raw TraceQL," +
		" keys are validated and values are quoted."
	return reason.ToolSpec{
		Name:        "traces",
		Description: desc,
		InputSchema: schema.Of[tracesInput](),
	}
}

// Run executes the search request against Tempo. It returns Live:true only if
// the response contains at least one trace span.
func (t *TracesTool) Run(ctx context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	var input tracesInput
	if err := json.Unmarshal(args, &input); err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("decode traces args: %w", err)
	}

	subject := t.Subjects.For(subjects.Coordinates{Namespace: input.Namespace, Labels: input.Labels})

	for k := range input.Labels {
		if !traceKeyRegex.MatchString(k) {
			return proposal.EvidenceRef{
				Tool:    "traces",
				Summary: fmt.Sprintf("invalid trace label key %q: must match identifier pattern", k),
				Live:    false,
				Subject: subject,
			}, nil
		}
	}

	traceQL := buildTraceQL(input.Labels, input.ErrorsOnly)

	lookback := input.Lookback
	if lookback == "" {
		lookback = "15m"
	}
	lb, err := time.ParseDuration(lookback)
	if err != nil {
		return proposal.EvidenceRef{
			Tool:    "traces",
			Query:   traceQL,
			Summary: fmt.Sprintf("invalid lookback: %v", err),
			Live:    false,
			Subject: subject,
		}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}

	end := time.Now()
	start := end.Add(-lb)

	u, err := url.Parse(t.BaseURL + "/api/search")
	if err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("parse url: %w", err)
	}
	u.RawQuery = url.Values{
		"q":     {traceQL},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"limit": {strconv.Itoa(limit)},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("new request: %w", err)
	}

	client := t.Client
	if client == nil {
		client = httpx.Client(httpx.DefaultBackendTimeout, nil)
	}

	resp, err := client.Do(req)
	if err != nil {
		return proposal.EvidenceRef{
			Tool:    "traces",
			Query:   traceQL,
			Summary: fmt.Sprintf("tempo request failed: %v", err),
			Live:    false,
			Subject: subject,
		}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return proposal.EvidenceRef{
			Tool:    "traces",
			Query:   traceQL,
			Summary: fmt.Sprintf("tempo returned status: %s", resp.Status),
			Live:    false,
			Subject: subject,
		}, nil
	}

	var body struct {
		Traces []struct {
			TraceID         string `json:"traceID"`
			RootServiceName string `json:"rootServiceName"`
			RootTraceName   string `json:"rootTraceName"`
		} `json:"traces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return proposal.EvidenceRef{
			Tool:    "traces",
			Query:   traceQL,
			Summary: fmt.Sprintf("decode tempo response: %v", err),
			Live:    false,
			Subject: subject,
		}, nil
	}

	if len(body.Traces) == 0 {
		return proposal.EvidenceRef{
			Tool:    "traces",
			Query:   traceQL,
			Summary: "no matching traces",
			Ref:     tempoRef(input.Namespace, input.Labels),
			Live:    false,
			Subject: subject,
		}, nil
	}

	last := body.Traces[len(body.Traces)-1]
	summary := fmt.Sprintf("%d trace(s); last root: %s/%s", len(body.Traces), last.RootServiceName, last.RootTraceName)

	return proposal.EvidenceRef{
		Tool:    "traces",
		Query:   traceQL,
		Summary: summary,
		Ref:     tempoRef(input.Namespace, input.Labels),
		Live:    true,
		Subject: subject,
	}, nil
}

// buildTraceQL assembles a TraceQL query from sorted attribute matchers and
// an optional status=error clause. Values are quoted via strconv.Quote.
func buildTraceQL(labels map[string]string, errorsOnly bool) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	matchers := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		matchers = append(matchers, k+"="+strconv.Quote(labels[k]))
	}
	if errorsOnly {
		matchers = append(matchers, "status=error")
	}

	if len(matchers) == 0 {
		return "{}"
	}
	return "{" + strings.Join(matchers, " && ") + "}"
}

// tempoRef renders a stable evidence ref for a namespace + trace label selector.
func tempoRef(namespace string, labels map[string]string) string {
	if len(labels) == 0 {
		return "tempo://" + namespace
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
	return "tempo://" + namespace + "/" + strings.Join(parts, ",")
}
