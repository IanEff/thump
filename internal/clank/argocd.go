package clank

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// applicationGVR is the ArgoCD Application CRD's GroupVersionResource.
var applicationGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// DefaultChangeLookback bounds how far back a sync still counts as a change
// worth scoring — four temporal half-lives (causal.go's temporalHalfLife),
// past which an event's recency component is under 0.07 and cannot move a
// Candidate's confidence enough to justify its weight in the SAO.
const DefaultChangeLookback = 2 * time.Hour

// ArgoChangeSource is the concrete ChangeSource: it reports each resource an
// Application recently synced as one change event, so a ChangeEvent.Target
// names a workload the topology graph also knows. Reporting the Application
// itself would name a GitOps unit no TopologySnapshot contains, and the
// causal scorer's topological component would never resolve.
type ArgoChangeSource struct {
	Client dynamic.Interface

	// Lookback bounds how old a sync may be and still be reported; zero
	// means DefaultChangeLookback. Without it every Application that ever
	// synced reports forever, and the SAO grows without bound.
	Lookback time.Duration

	Now func() time.Time
}

// Changes lists Applications across all namespaces — an
// Application-of-Applications parent or an ApplicationSet generator can
// land a child Application anywhere, so a fixed namespace would silently
// miss whichever layout a given cluster uses.
func (a ArgoChangeSource) Changes(ctx context.Context, _ signal.Detection) (proposal.ChangeSnapshot, error) {
	list, err := a.Client.Resource(applicationGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return proposal.ChangeSnapshot{}, fmt.Errorf("list argocd applications: %w", err)
	}

	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	lookback := a.Lookback
	if lookback <= 0 {
		lookback = DefaultChangeLookback
	}

	var snap proposal.ChangeSnapshot
	for _, app := range list.Items {
		snap.Events = append(snap.Events, appEvents(app, now(), lookback)...)
	}
	return snap, nil
}

// appEvents turns one Application into one ChangeEvent per resource it
// manages, all sharing the sync's revision and age — the fan-out is what
// puts a name the topology graph recognizes into ChangeEvent.Target. An
// Application whose last sync did not succeed, cannot be dated, or finished
// outside lookback yields nothing.
func appEvents(app unstructured.Unstructured, now time.Time, lookback time.Duration) []proposal.ChangeEvent {
	phase, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "phase")
	if phase != "Succeeded" {
		return nil
	}
	finishedAtStr, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "finishedAt")
	finishedAt, err := time.Parse(time.RFC3339, finishedAtStr)
	if err != nil {
		return nil
	}
	age := now.Sub(finishedAt)
	if age > lookback {
		return nil
	}

	automated, _, _ := unstructured.NestedBool(app.Object, "status", "operationState", "operation", "initiatedBy", "automated")
	revision, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "operation", "sync", "revision")

	typ := "deploy"
	if automated {
		typ = "rollback"
	}

	// An Application reporting no managed resources still changed something,
	// so it falls back to naming itself rather than vanishing — the score
	// will carry InTopology false and stay out of the confidence product.
	targets := managedResourceNames(app)
	if len(targets) == 0 {
		targets = []string{app.GetName()}
	}

	events := make([]proposal.ChangeEvent, 0, len(targets))
	for _, target := range targets {
		events = append(events, proposal.ChangeEvent{
			ID:                  revision,
			Type:                typ,
			Target:              target,
			Age:                 age,
			HistoricalStaleness: age,
		})
	}
	return events
}

// managedResourceNames reads the resource inventory ArgoCD already publishes
// on every Application, deduplicated by name — the same workload appearing
// as both a Deployment and a Service is one target, not two.
func managedResourceNames(app unstructured.Unstructured) []string {
	resources, _, err := unstructured.NestedSlice(app.Object, "status", "resources")
	if err != nil {
		return nil
	}

	seen := make(map[string]bool, len(resources))
	var names []string
	for _, r := range resources {
		res, ok := r.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(res, "name")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}
