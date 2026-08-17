package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ErrBuildInstantQuery wraps a failure to construct an instant-query
// request — a base URL that doesn't parse, or something
// http.NewRequestWithContext rejects — before any network call happens.
var ErrBuildInstantQuery = errors.New("build instant query request")

// ErrDecodeInstantResult wraps a failure to decode a 200 response body into
// the shape InstantResult expects.
var ErrDecodeInstantResult = errors.New("decode instant query result")

// StatusError reports a non-200 response to an instant query, carrying the
// response's Status text verbatim — callers disagree on what a bad status
// should become (an unknown-state enum, a bare bool, an audit-trail
// citation string), so InstantQuery makes no judgment call about it.
type StatusError struct{ Status string }

func (e *StatusError) Error() string { return "instant query: status " + e.Status }

// InstantResult is the decoded body of a Prometheus-shaped instant-query
// response (/api/v1/query) — the same nesting whether the backend is
// Prometheus itself or an exporter that speaks its wire format, like
// Hubble's.
type InstantResult struct {
	Data struct {
		Result []struct {
			Metric map[string]string  `json:"metric,omitempty"`
			Value  [2]json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// InstantQuery issues one GET to baseURL+"/api/v1/query" for query and
// decodes a 200 response into an InstantResult. client defaults to
// Client(DefaultBackendTimeout, nil) when nil. It makes no judgment about
// an empty Result vector, a non-numeric value, or what any failure means —
// those interpretations differ across every caller, so decoding
// result.Data.Result[0].Value[1] and deciding what "no data" means is left
// to them. Check a returned error with errors.Is(err, ErrBuildInstantQuery)
// or errors.AsType[*StatusError](err) when the distinction matters; a
// caller that treats every failure the same can just test err != nil.
func InstantQuery(ctx context.Context, client *http.Client, baseURL, query string) (InstantResult, error) {
	u, err := url.Parse(baseURL + "/api/v1/query")
	if err != nil {
		return InstantResult{}, fmt.Errorf("%w: %w", ErrBuildInstantQuery, err)
	}
	u.RawQuery = url.Values{"query": {query}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return InstantResult{}, fmt.Errorf("%w: %w", ErrBuildInstantQuery, err)
	}
	if client == nil {
		client = Client(DefaultBackendTimeout, nil)
	}
	resp, err := client.Do(req)
	if err != nil {
		return InstantResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return InstantResult{}, &StatusError{Status: resp.Status}
	}

	var result InstantResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return InstantResult{}, fmt.Errorf("%w: %w", ErrDecodeInstantResult, err)
	}
	return result, nil
}
