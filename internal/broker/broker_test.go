package broker_test

import (
	"testing"

	"github.com/ianeff/thump/internal/broker"
)

func TestDurableFor_RoutesApprovalsToANewHissDurable(t *testing.T) {
	t.Parallel()
	got := broker.DurableFor("thump.approvals")
	if got == "" {
		t.Fatal("want a durable name for thump.approvals, got none provisioned")
	}
	if got == broker.DurableFor("thump.proposals") {
		t.Errorf("thump.approvals must not reuse thump.proposals's durable name (%q) — see DurableFor's doc comment on why reuse rebinds an existing consumer", got)
	}
}
