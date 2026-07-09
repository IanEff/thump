package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type kubeInput struct {
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
}

// KubeTool is the production implementation of the "kube" tool: read-only
// cluster queries (currently just listing pods in a namespace), so the model
// can corroborate a signal against live cluster state without clank ever
// holding a client that can mutate anything.
type KubeTool struct {
	Client kubernetes.Interface
}

// Implement the Tool interface.
var _ Tool = (*KubeTool)(nil)

// Spec advertises the "kube" tool — resource currently supports only "pods".
func (k *KubeTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "kube",
		Description: "read-only kubernetes resource query (supports resource: 'pods')",
		InputSchema: SchemaOf[kubeInput](),
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

	var summary string

	switch input.Resource {
	case "pods":
		list, err := k.Client.CoreV1().Pods(input.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return proposal.EvidenceRef{
				Tool:    "kube",
				Query:   string(args),
				Summary: fmt.Sprintf("failed to list pods: %v", err),
				Live:    false,
			}, nil
		}
		if len(list.Items) == 0 {
			return proposal.EvidenceRef{
				Tool:    "kube",
				Query:   string(args),
				Summary: "no pods found",
				Live:    false,
			}, nil
		}
		var statuses []string
		for _, p := range list.Items {
			statuses = append(statuses, fmt.Sprintf("%s (%s)", p.Name, p.Status.Phase))
		}
		summary = strings.Join(statuses, ", ")
	default:
		return proposal.EvidenceRef{
			Tool:    "kube",
			Query:   string(args),
			Summary: fmt.Sprintf("unsupported resource: %s", input.Resource),
			Live:    false,
		}, nil
	}

	return proposal.EvidenceRef{
		Tool:    "kube",
		Query:   string(args),
		Summary: summary,
		Ref:     fmt.Sprintf("kube://%s/%s", input.Namespace, input.Resource),
		Live:    true,
	}, nil
}
