package hiss

import (
	"context"
	"time"

	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/nats-io/nats.go/jetstream"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// HandleForTest exposes Transport.handle to hiss_test without handle
// becoming part of hiss's real API. Only compiled under `go test` — the
// _test.go suffix keeps it out of the shipped binary. Mirrors
// internal/clank/export_test.go and internal/rattle/export_test.go.
func (tr *Transport) HandleForTest(ctx context.Context, ps proposal.Set, heartbeat func()) error {
	return tr.handle(ctx, ps, heartbeat)
}

func (tr *Transport) ApproveHandlerForTest(ctx context.Context, a approval.Approval, heartbeat func()) error {
	return tr.approveHandler(ctx, a, heartbeat)
}

func RebuildHoldsForTest(ctx context.Context, js jetstream.JetStream) (*PendingHolds, error) {
	return rebuildHolds(ctx, js)
}

func TranslateDecisionForTest(spec ApprovalRequestSpec, approvedBy string, now time.Time) (approval.Approval, bool, error) {
	return translateDecision(spec, approvedBy, now)
}

func NewApprovalRequestSpecForTest(held decision.Governed) ApprovalRequestSpec {
	return newApprovalRequestSpec(held)
}

func ApprovalRequestNameForTest(signalRef string) string {
	return approvalRequestName(signalRef)
}

func (c *ApprovalRequestController) ReconcileForTest(ctx context.Context, u *unstructured.Unstructured) error {
	return c.reconcile(ctx, u)
}

// ReconcileObjForTest drives the informer's own entry point, so a test sees
// the sweep and the reconcile in the order a resync delivers them.
func (c *ApprovalRequestController) ReconcileObjForTest(ctx context.Context, obj any) {
	c.reconcileObj(ctx, obj)
}
