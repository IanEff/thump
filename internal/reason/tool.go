package reason

import (
	"context"
	"encoding/json"

	"github.com/ianeff/thump/api/v1/proposal"
)

// Tool is a read-only evidence source the reason loop can call mid-turn —
// telemetry, logs, cluster state, or case-base retrieval. Run returns a
// proposal.EvidenceRef: a one-line digest plus a backend ref to re-fetch,
// never the raw payload. EvidenceRef has no Raw field and never will — raw
// data cannot enter the conversation history the model reasons over.
type Tool interface {
	Spec() ToolSpec
	Run(ctx context.Context, args json.RawMessage) (proposal.EvidenceRef, error)
}
