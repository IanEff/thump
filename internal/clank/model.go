package clank

import "context"

type Model interface {
	Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error)
}

type ToolSpec struct {
	Name        string
	Description string
}
