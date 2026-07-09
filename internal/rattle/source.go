package rattle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// PromSource fetches burn-rate windows from a Sloth-instrumented
// Prometheus — rattle's only Source implementation reading a live backend
// rather than a test fake.
type PromSource struct {
	BaseURL string
	Client  *http.Client
	Step    time.Duration // sample spacing; BurnSamples defaults to 1m when zero
	Window  time.Duration // how far back to query; BurnSamples defaults to 15m when zero
}

// NewPromSource returns a PromSource with the default 1-minute step, 15-minute
// window, and http.DefaultClient.
func NewPromSource(baseURL string) *PromSource {
	return &PromSource{BaseURL: baseURL, Client: http.DefaultClient, Step: time.Minute, Window: 15 * time.Minute}
}

type promRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"` // "matrix"
		Result     []struct {
			Metric map[string]string    `json:"metric"`
			Values [][2]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// BurnSamples queries Sloth's slo:current_burn_rate:ratio series directly —
// no manual errorRatio/(1-objective) conversion, since Sloth already records
// the burn rate as a ratio. A result set with no matching series is a valid
// empty window, not an error: Prometheus simply has nothing for this SLO yet.
func (p *PromSource) BurnSamples(ctx context.Context, slo SLO) ([]Sample, error) {
	step := p.Step
	if step == 0 {
		step = time.Minute
	}
	window := p.Window
	if window == 0 {
		window = 15 * time.Minute
	}
	end := time.Now()
	start := end.Add(-window)
	// Sloth records the burn rate DIRECTLY as slo:current_burn_rate:ratio — no manual
	// (errorRatio / (1-objective)) conversion. Select the one series by sloth_id.
	q := fmt.Sprintf(`slo:current_burn_rate:ratio{sloth_id=%q}`, slo.ID)

	u, err := url.Parse(p.BaseURL + "/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("prom base url: %w", err)
	}
	u.RawQuery = url.Values{
		"query": {q},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.Itoa(int(step.Seconds())) + "s"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build prom request: %w", err)
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus returned %s", resp.Status)
	}

	var body promRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode prom body: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus status %q, want success", body.Status)
	}
	if len(body.Data.Result) == 0 {
		return nil, nil // no series for this SLO — an empty window, not an error
	}
	return samplesFromValues(body.Data.Result[0].Values)
}

func samplesFromValues(values [][2]json.RawMessage) ([]Sample, error) {
	out := make([]Sample, 0, len(values))
	for _, pair := range values {
		var ts float64
		if err := json.Unmarshal(pair[0], &ts); err != nil {
			return nil, fmt.Errorf("parse sample timestamp: %w", err)
		}
		var raw string
		if err := json.Unmarshal(pair[1], &raw); err != nil {
			return nil, fmt.Errorf("parse sample value: %w", err)
		}
		rate, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("sample value %q not a float: %w", raw, err)
		}
		out = append(out, Sample{T: time.Unix(int64(ts), 0).UTC(), BurnRate: rate})
	}
	return out, nil
}
