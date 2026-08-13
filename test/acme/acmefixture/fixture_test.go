package acmefixture_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/test/acme/acmefixture"
)

func TestRegisteredMetricNames_ReturnsNonEmptyDeduplicated(t *testing.T) {
	t.Parallel()
	names := acmefixture.RegisteredMetricNames()
	if len(names) == 0 {
		t.Fatal("RegisteredMetricNames must return at least one name")
	}

	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			t.Errorf("duplicate metric name: %s", n)
		}
		seen[n] = true
	}

	for _, want := range []string{
		"acme_api_requests_total",
		"acme_db_connections_active",
		"acme_db_connections_max",
	} {
		if diff := cmp.Diff(true, slices.Contains(names, want)); diff != "" {
			t.Errorf("missing expected metric %s\n%s", want, diff)
		}
	}
}
