package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type kubeInput struct {
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
}

type KubeTool struct {
	Client kubernetes.Interface
}

// Implement the Tool interface.
var _ Tool = (*KubeTool)(nil)

func (k *KubeTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "kube",
		Description: "read-only kubernetes resource query (supports resource: 'pods')",
		InputSchema: SchemaOf[kubeInput](),
	}
}

func (k *KubeTool) Run(ctx context.Context, args json.RawMessage) (EvidenceRef, error) {
	var input kubeInput
	if err := json.Unmarshal(args, &input); err != nil {
		return EvidenceRef{}, fmt.Errorf("decode kube args: %w", err)
	}

	var summary string

	switch input.Resource {
	case "pods":
		list, err := k.Client.CoreV1().Pods(input.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return EvidenceRef{
				Tool:    "kube",
				Query:   string(args),
				Summary: fmt.Sprintf("failed to list pods: %v", err),
				Live:    false,
			}, nil
		}
		if len(list.Items) == 0 {
			return EvidenceRef{
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
		return EvidenceRef{
			Tool:    "kube",
			Query:   string(args),
			Summary: fmt.Sprintf("unsupported resource: %s", input.Resource),
			Live:    false,
		}, nil
	}

	return EvidenceRef{
		Tool:    "kube",
		Query:   string(args),
		Summary: summary,
		Ref:     fmt.Sprintf("kube://%s/%s", input.Namespace, input.Resource),
		Live:    true,
	}, nil
}
