package clank

import (
	"context"
	"encoding/json"
)

type Tool interface {
	Spec() ToolSpec
	Run(ctx context.Context, args json.RawMessage) (EvidenceRef, error)
}
