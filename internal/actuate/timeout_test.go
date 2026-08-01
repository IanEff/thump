package actuate_test

import (
	"testing"

	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
)

func TestActuateTimeout_FinishesInsideTheAckWaitTheBrokerProvisions(t *testing.T) {
	t.Parallel()
	// The highest-consequence case in the tree: a redelivered decision is a
	// second execution of an already-running Order against the live cluster,
	// and Runner.Run has no heartbeat to hold the deadline open.
	ctx := t.Context()
	ackWait, err := broker.ProvisionedAckWait(ctx, natstest.New(t), "thump.decisions")
	if err != nil {
		t.Fatal(err)
	}
	if got := actuate.ActuateTimeoutForTest(); got >= ackWait {
		t.Errorf("actuateTimeout is %s against a provisioned AckWait of %s — the actuator must fail before the server redelivers, or one approved Order executes twice", got, ackWait)
	}
}
