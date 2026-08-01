package hiss_test

import (
	"testing"

	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/natstest"
)

func TestApprovalRequestTimeout_FinishesInsideTheAckWaitTheBrokerProvisions(t *testing.T) {
	t.Parallel()
	// A comment asserts this relationship and nothing enforces it. The
	// controller's apiserver call runs inside a proposals handler, so a
	// timeout raised past AckWait gets the message redelivered while the
	// first attempt is still running — a second controller pass against the
	// same ApprovalRequest, with no heartbeat to hold the deadline open.
	ctx := t.Context()
	ackWait, err := broker.ProvisionedAckWait(ctx, natstest.New(t), "thump.proposals")
	if err != nil {
		t.Fatal(err)
	}
	if got := hiss.ApprovalRequestTimeoutForTest(); got >= ackWait {
		t.Errorf("approvalRequestTimeout is %s against a provisioned AckWait of %s — the handler must fail before the server redelivers, or hiss processes one proposal twice", got, ackWait)
	}
}
