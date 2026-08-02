package hiss

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/otelx"
	"github.com/ianeff/thump/internal/publish"
)

// Transport is hiss's directory-poll seam: it watches Inbox for proposal.Set
// files, governs each one through Authority.Evaluate, and publishes the
// resulting decision.Governed envelope. It's the keyless fake the seam
// tests drive without a broker; hiss.go's Main runs the NATS branch instead
// in production.
type Transport struct {
	Inbox     string                               // directory globbed for *.yaml proposal.Set files
	Pub       publish.Publisher[decision.Governed] // destination for governed decisions — thump.decisions in production
	Policy    Policy                               // the floors, ceilings, and freeze windows Authority.Evaluate governs against
	Log       *DecisionLog                         // every Decision reached, queryable by ByVerdict
	Holds     *PendingHolds                        // fingerprints hiss has held
	Approvals ApprovalRequests                     // creates the CR for a hold; nil in the offline dir-poll path, same nil-safety as Tracer/Stages
	Now       func() time.Time                     // overridable clock for deterministic tests; nil means time.Now
	Tracer    trace.Tracer                         // spans "govern" under whatever trace ctx already carries; nil-safe via tracer()
	Stages    *beat.StageRecorder                  // RED metrics for "govern" — nil-safe, same discipline as Tracer
}

// Tick performs one poll pass: list Inbox, decode each file, evaluate it
// through handle, and archive or quarantine the result. A file that fails to
// unmarshal is quarantined, not deleted, and does not block the rest of the
// pass — one poison proposal.Set can't stall every other one behind it.
func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return beat.DrainDir(tr.Inbox, "hiss", func(path string, ps proposal.Set) error {
		if err := tr.handle(ctx, ps, nil); err != nil {
			return fmt.Errorf("hiss: handle %s: %w", path, err)
		}
		if err := beat.Disposition(tr.Inbox, path, "processed"); err != nil {
			return fmt.Errorf("hiss: archive %s: %w", path, err)
		}
		return nil
	})
}

// handle evaluates one ProposalSet and publishes the Governed decision — the
// transport-independent core. Tick calls it after decoding a file; the NATS
// handler calls it after decoding a message. Same brain, two feeders.
// Evaluate is fast enough that it never needs heartbeat, unlike clank's
// reason loop — accepted only to satisfy broker.Handler[T]'s shape.
func (tr *Transport) handle(ctx context.Context, ps proposal.Set, _ func()) error {
	now := beat.Clock(tr.Now)
	var d decision.Decision
	_ = beat.Stage(ctx, otelx.TracerOrNoop(tr.Tracer), tr.Stages, "govern", func(context.Context) error {
		var auth Authority
		d = auth.Evaluate(ps, tr.Policy, now())
		return nil
	})
	if d.Verdict == decision.VerdictHold && tr.Holds != nil {
		held := decision.Governed{Decision: d, Set: ps}
		tr.Holds.Record(held)
		if tr.Approvals != nil {
			if err := tr.Approvals.Create(ctx, held); err != nil {
				return fmt.Errorf("hiss: create approvalrequest for %s: %w", d.SignalRef, err)
			}
		}
	}
	tr.Log.Record(d)
	rec, _ := recommended(ps)
	slog.Info("decision", "fingerprint", ps.SignalRef, "verdict", d.Verdict, "reasons", d.Reasons,
		"requestedBand", d.RequestedBand, "grantedBand", d.GrantedBand, "contractRef", ps.ContractRefFor(d.CandidateRef),
		"confidence", rec.Confidence, "floorApplied", d.FloorApplied)
	return tr.Pub.Publish(ctx, "thump.decisions", decision.Governed{Decision: d, Set: ps})
}

func (tr *Transport) approveHandler(ctx context.Context, a approval.Approval, _ func()) error {
	held, ok := tr.Holds.Take(a.SignalRef)
	if !ok {
		slog.Warn("approval arrived for an unheld fingerprint", "signalRef", a.SignalRef)
		return nil
	}

	now := beat.Clock(tr.Now)

	d := held.Decision
	d.ID = fmt.Sprintf("dec:%s:%d", d.SignalRef, now().Unix())
	d.Verdict = decision.VerdictApproved
	d.GrantedBand = d.RequestedBand
	d.Reasons = nil // the risk-ceiling reason that earned the hold is satisfied now
	d.Approver = a.Approver
	d.PolicyVersion = tr.Policy.Version
	d.EvaluatedAt = now()

	if err := d.Auditable(); err != nil {
		return fmt.Errorf("hiss: re-stamped decision not auditable: %w", err)
	}
	if err := tr.Pub.Publish(ctx, "thump.decisions", decision.Governed{Decision: d, Set: held.Set}); err != nil {
		return fmt.Errorf("hiss: publish re-issued decision for %s: %w", a.SignalRef, err)
	}
	tr.Log.Record(d)
	slog.Info("approved", "signalRef", a.SignalRef, "approver", a.Approver, "grantedBand", d.GrantedBand)
	return nil
}
