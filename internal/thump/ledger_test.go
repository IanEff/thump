package thump_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

func TestOutcomeLog_WhatThumpDidIsQueryable(t *testing.T) {
	t.Parallel()
	log := thump.NewOutcomeLog()

	rendered := goldenOutcome()
	failed := goldenOutcome()
	failed.ID, failed.Result, failed.Error = "out:fail", outcome.ResultFailure, "apply timed out"
	failed.ExecutedAt = frozenNow().Add(time.Minute)

	for _, o := range []outcome.Outcome{rendered, failed} {
		log.Record(o)
	}

	if diff := cmp.Diff([]outcome.Outcome{rendered}, log.ByResult(outcome.ResultRendered)); diff != "" {
		t.Error("the rendered pile answered wrong (-want +got)", diff)
	}
	if diff := cmp.Diff([]outcome.Outcome{failed}, log.ByResult(outcome.ResultFailure)); diff != "" {
		t.Error("the failure pile answered wrong (-want +got)", diff)
	}
	// Since is a half-open window on ExecutedAt: strictly-after.
	if diff := cmp.Diff([]outcome.Outcome{failed}, log.Since(frozenNow())); diff != "" {
		t.Error("Since must return outcomes strictly after the cut (-want +got)", diff)
	}
}
