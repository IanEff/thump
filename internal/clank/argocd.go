package clank

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/subjects"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// applicationGVR is the ArgoCD Application CRD's GroupVersionResource.
var applicationGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// DefaultChangeLookback bounds how far back a sync still counts as a change
// worth scoring — four half-lives of ScoringWeights.RecencyHalfLife, past
// which an event's recency component is under 0.07 and cannot move a
// Candidate's confidence enough to justify its weight in the SAO.
const DefaultChangeLookback = 2 * time.Hour

// ArgoChangeSource is the concrete ChangeSource: it reports each resource an
// Application recently synced as one change event, resolved through Subjects so
// a ChangeEvent.Target names a topology node rather than a Kubernetes object.
// The two vocabularies do not coincide — ArgoCD reports the CephBlockPool
// "replicapool" where the catalog holds the entity "cephblockpool" — and both
// are strings, so an unresolved target joins against nothing and scores every
// event out of topology while the suite stays green.
type ArgoChangeSource struct {
	Client dynamic.Interface

	// Subjects is the same authored index the evidence tools resolve through,
	// so a rig states once which cluster coordinates belong to which node.
	// Empty means no resource resolves and every event scores out of topology.
	Subjects subjects.SubjectIndex

	// ChangeLookback bounds how old a sync may be and still be reported;
	// zero means DefaultChangeLookback. Without it every Application that
	// ever synced reports forever, and the SAO grows without bound.
	ChangeLookback time.Duration

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

	now := beat.Clock(a.Now)
	lookback := a.ChangeLookback
	if lookback <= 0 {
		lookback = DefaultChangeLookback
	}

	var snap proposal.ChangeSnapshot
	for _, app := range list.Items {
		snap.Events = append(snap.Events, appEvents(app, now(), lookback, a.Subjects)...)
	}
	return snap, nil
}

// appEvents turns one Application into one ChangeEvent per topology node it
// touched, all sharing the sync's revision and age — the fan-out is what puts
// a name the topology graph recognizes into ChangeEvent.Target. An Application
// whose last sync did not succeed, cannot be dated, or finished outside
// lookback yields nothing.
func appEvents(app unstructured.Unstructured, now time.Time, lookback time.Duration, idx subjects.SubjectIndex) []proposal.ChangeEvent {
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
	targets := managedSubjects(app, idx)
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

// managedSubjects reads the resource inventory ArgoCD already publishes on
// every Application and resolves each entry to a topology node, deduplicated —
// several Kubernetes objects belonging to one node are one target, not several,
// so a synced Application does not report the same node once per manifest. An
// entry no rule claims is dropped: it names an object the graph cannot place,
// and an unresolved Kubernetes name on a ChangeEvent.Target is
// indistinguishable from a node name that simply went missing.
func managedSubjects(app unstructured.Unstructured, idx subjects.SubjectIndex) []string {
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
		kind, _, _ := unstructured.NestedString(res, "kind")
		namespace, _, _ := unstructured.NestedString(res, "namespace")

		subject := idx.For(subjects.Coordinates{Namespace: namespace, Kind: kind, Name: name})
		if subject == "" || seen[subject] {
			continue
		}
		seen[subject] = true
		names = append(names, subject)
	}
	return names
}
