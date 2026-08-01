package hiss

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
)

// ApprovalRequestSpec is the ApprovalRequest CR's spec: hiss writes
// SignalRef, Action, Band and Reasons when it creates the CR from a held
// Governed; a human writes only Decision, via kubectl patch. Everything else
// is rejected at the API server, not here. The authenticated approver never
// appears in spec or status — a CRD's status subresource resets .status to
// its old value on every UPDATE that isn't itself targeted at /status
// (exactly the request a plain kubectl patch sends), so a MutatingAdmissionPolicy
// stamps it into metadata.annotations instead, where the approvalrequest
// controller reads it back. JSON tags match the CRD's OpenAPI property
// names — approvalrequest_controller.go converts through them via
// unstructured content, never through a generated clientset.
type ApprovalRequestSpec struct {
	SignalRef string   `json:"signalRef"`
	Action    string   `json:"action"`
	Band      string   `json:"band"`
	Reasons   []string `json:"reasons,omitempty"`
	Decision  string   `json:"decision,omitempty"` // "" or "approve"; bypassing the risk gate is trim force's job, never this resource's
}

// ApprovalRequestStatus is the CR's controller-owned half: the controller
// writes Phase and DecidedAt once it has translated Decision, so a resync
// redelivering the same object is a no-op rather than a repeat publish.
type ApprovalRequestStatus struct {
	Phase     string    `json:"phase,omitempty"` // "" or "Processed"
	DecidedAt time.Time `json:"decidedAt"`
}

// ApprovalRequests is hiss's seam onto the one ApprovalRequest CR it
// creates per hold — narrow enough that transport.go can depend on it
// without importing client-go itself; approvalrequest_controller.go is the
// only file that implements it.
type ApprovalRequests interface {
	Create(ctx context.Context, held decision.Governed) error
}

// translateDecision turns a patched ApprovalRequestSpec into the ack hiss
// publishes on thump.approvals. approvedBy is the authenticated Kubernetes
// subject that made the patch — resolved upstream of this function, never
// read from spec, which a subject could set to any string. ok is false when
// no human has acted yet, which is most reconcile events, not an error.
func translateDecision(spec ApprovalRequestSpec, approvedBy string, now time.Time) (approval.Approval, bool, error) {
	switch spec.Decision {
	case "":
		return approval.Approval{}, false, nil
	case "approve":
		return approval.Approval{
			SignalRef:  spec.SignalRef,
			Approver:   approvedBy,
			ApprovedAt: now,
		}, true, nil
	default:
		return approval.Approval{}, false, fmt.Errorf("hiss: unrecognized ApprovalRequest decision %q", spec.Decision)
	}
}

// newApprovalRequestSpec builds the ApprovalRequest CR's spec from a
// Governed hiss just held - the reverse of translateDecision, and
// the content a human reads with kubectl describe before deciding.
func newApprovalRequestSpec(held decision.Governed) ApprovalRequestSpec {
	return ApprovalRequestSpec{
		SignalRef: held.Decision.SignalRef,
		Action:    held.Set.ContractRefFor(held.Decision.CandidateRef),
		Band:      string(held.Decision.RequestedBand),
		Reasons:   held.Decision.Reasons,
	}
}
