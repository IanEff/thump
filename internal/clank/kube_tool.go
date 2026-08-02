package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/mask"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/schema"
	"github.com/ianeff/thump/internal/subjects"
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
	return reason.ToolSpec{
		Name: "kube",
		Description: "read-only kubernetes resource query (supports resource: 'pods'). " +
			"selector is an optional map of label equality pairs, e.g. {\"app\": \"cart\"} — " +
			"narrow to one workload with it; an unnarrowed namespace query spans every " +
			"workload in it and cannot be evidence about any one of them.",
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
			mask.RegisterIdentifier(ctx, p.Name)
			statuses = append(statuses, fmt.Sprintf("%s (%s)", p.Name, p.Status.Phase))
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
