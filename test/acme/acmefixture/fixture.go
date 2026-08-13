// Package acmefixture is the Go-side truth about what the acme workload
// emits — metric names the Python server in deploy/dev/manifests/acme.yaml
// registers, declared here so a test can assert the dev profile's
// evidence-queries.yaml cites series the app actually produces, rather
// than trusting a string match between two YAML files.
package acmefixture

// RegisteredMetricNames returns the Prometheus metric names the acme
// fixture server exposes on /metrics. The list is hand-maintained in
// lockstep with the Python server embedded in
// deploy/dev/manifests/acme.yaml's acme-api-server ConfigMap — a drift
// between the two is caught by
// TestDevProfile_AuthorsEveryAcmeSubjectTheAppActuallyEmits.
func RegisteredMetricNames() []string {
	return []string{
		"acme_api_requests_total",
		"acme_db_connections_active",
		"acme_db_connections_max",
	}
}
