package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/schema"
	"github.com/ianeff/thump/internal/subjects"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type kubeInput struct {
	Resource  string            `json:"resource"`
	Namespace string            `json:"namespace"`
	Selector  map[string]string `json:"selector,omitempty"` // label equality pairs; rendered server-side, never spliced into a selector string raw
}

// KubeTool is the production implementation of the "kube" tool: read-only
// cluster queries (currently just listing pods in a namespace), so the model
// can corroborate a signal against live cluster state without clank ever
// holding a client that can mutate anything.
type KubeTool struct {
	Client kubernetes.Interface
	// Subjects resolves a query's namespace and selector to the topology node
	// it is evidence about (EvidenceRef.Subject). Coordinates no rule claims
	// stamp no Subject — a namespace holding several nodes is evidence about
	// none of them, so it can corroborate but never ground.
	Subjects subjects.SubjectIndex
}

// Implement the reason.Tool interface.
var _ reason.Tool = (*KubeTool)(nil)

// Spec advertises the "kube" tool — resource currently supports only "pods".
func (k *KubeTool) Spec() reason.ToolSpec {
	desc := "read-only kubernetes resource query (supports resource: 'pods')." +
		" selector is an optional map of label equality pairs — narrow to one" +
		" workload with it; an unnarrowed namespace query spans every workload in" +
		" it and cannot be evidence about any one of them."
	if sels := k.Subjects.Selectors("kube"); len(sels) > 0 {
		desc += " Authored selectors: " + strings.Join(sels, "; ") + "."
	}
	return reason.ToolSpec{
		Name:        "kube",
		Description: desc,
		InputSchema: schema.Of[kubeInput](),
	}
}

// Run lists the requested resource and folds it into a one-line summary —
// pod name and phase, joined — never the raw object list. Live is true only
// when the query returns at least one item.
func (k *KubeTool) Run(ctx context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	var input kubeInput
	if err := json.Unmarshal(args, &input); err != nil {
		return proposal.EvidenceRef{}, fmt.Errorf("decode kube args: %w", err)
	}
	subject := k.Subjects.For(subjects.Coordinates{Namespace: input.Namespace, Labels: input.Selector})

	var summary string

	switch input.Resource {
	case "pods":
		opts := metav1.ListOptions{LabelSelector: labels.Set(input.Selector).String()}
		list, err := k.Client.CoreV1().Pods(input.Namespace).List(ctx, opts)
		if err != nil {
			return proposal.EvidenceRef{
				Tool:    "kube",
				Query:   string(args),
				Summary: fmt.Sprintf("failed to list pods: %v", err),
				Live:    false,
				Subject: subject,
			}, nil
		}
		if len(list.Items) == 0 {
			return proposal.EvidenceRef{
				Tool:    "kube",
				Query:   string(args),
				Summary: "no pods found",
				Live:    false,
				Subject: subject,
			}, nil
		}
		var statuses []string
		for _, p := range list.Items {
			statuses = append(statuses, podDigest(p))
		}
		summary = strings.Join(statuses, ", ")
	default:
		return proposal.EvidenceRef{
			Tool:    "kube",
			Query:   string(args),
			Summary: fmt.Sprintf("unsupported resource: %s", input.Resource),
			Live:    false,
			Subject: subject,
		}, nil
	}

	return proposal.EvidenceRef{
		Tool:    "kube",
		Query:   string(args),
		Summary: summary,
		Ref:     fmt.Sprintf("kube://%s/%s", input.Namespace, input.Resource),
		Live:    true,
		Subject: subject,
	}, nil
}

// podDigest renders one pod as the shortest line that can still distinguish a
// healthy pod from a failing one. A pod whose containers are all healthy is
// rendered as "name (Phase)" so the common healthy case stays byte-stable.
func podDigest(p corev1.Pod) string {
	var (
		restarts      int32
		lastReason    string
		waitingReason string
		hasNotReady   bool
	)
	for _, cs := range p.Status.ContainerStatuses {
		restarts += cs.RestartCount
		if lastReason == "" && cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason != "" {
			lastReason = cs.LastTerminationState.Terminated.Reason
		}
		if waitingReason == "" && cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			waitingReason = cs.State.Waiting.Reason
		}
		if !cs.Ready {
			hasNotReady = true
		}
	}

	clauses := []string{string(p.Status.Phase)}
	if restarts > 0 {
		clauses = append(clauses, fmt.Sprintf("restarts=%d", restarts))
	}
	if lastReason != "" {
		clauses = append(clauses, fmt.Sprintf("last: %s", lastReason))
	}
	if waitingReason != "" {
		clauses = append(clauses, fmt.Sprintf("waiting: %s", waitingReason))
	}
	if hasNotReady && p.Status.Phase == corev1.PodRunning && waitingReason == "" {
		clauses = append(clauses, "notReady")
	}

	return fmt.Sprintf("%s (%s)", p.Name, strings.Join(clauses, ", "))
}
