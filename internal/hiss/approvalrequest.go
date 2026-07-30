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
// Governed; a human writes only Decision, via kubectl patch. Everything
// else is rejected at the API server, not here. JSON tags match the CRD's
// OpenAPI property names — approvalrequest_controller.go converts through
// them via unstructured content, never through a generated clientset.
type ApprovalRequestSpec struct {
	SignalRef string   `json:"signalRef"`
	Action    string   `json:"action"`
	Band      string   `json:"band"`
	Reasons   []string `json:"reasons,omitempty"`
	Decision  string   `json:"decision,omitempty"` // "" or "approve" — anything else fails translateDecision
}

// ApprovalRequestStatus is the CR's controller-owned half. ApprovedBy is
// stamped by a MutatingAdmissionPolicy at admission time, from the patch
// request's authenticated UserInfo — hiss's controller only ever reads it,
// never writes it, so there's no race between the policy and the reconcile
// loop over who owns the field. Phase and DecidedAt are the reverse: the
// controller writes them once it has translated Decision, so a resync
// redelivering the same object is a no-op rather than a repeat publish.
type ApprovalRequestStatus struct {
	ApprovedBy string    `json:"approvedBy,omitempty"`
	Phase      string    `json:"phase,omitempty"` // "" or "Processed"
	DecidedAt  time.Time `json:"decidedAt,omitempty"`
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

// forceDecision mirrors trim force's break-glass path (runForce in
// internal/trim/trim.go) as a pure function: it re-stamps a held Governed as
// approved, forced, and attributed to forcedBy. PolicyVersion is left
// untouched — the held decision already carries the version it was
// evaluated under, and forcing it through doesn't re-evaluate anything.
func forceDecision(held decision.Governed, forcedBy string, now time.Time) (decision.Governed, error) {
	d := held.Decision
	d.ID = fmt.Sprintf("dec:%s:force:%d", d.SignalRef, now.Unix())
	d.Verdict = decision.VerdictApproved
	d.GrantedBand = d.RequestedBand
	d.Reasons = nil
	d.Forced = true
	d.Operator = forcedBy
	d.EvaluatedAt = now

	if err := d.Auditable(); err != nil {
		return decision.Governed{}, fmt.Errorf("hiss: forced decision not auditable: %w", err)
	}
	return decision.Governed{Decision: d, Set: held.Set}, nil
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
