package ledger_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/ledger"
)

type event struct {
	name string
	at   time.Time
}

func TestLog_RecordsAndWindowsAndFilters(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	log := ledger.NewLog(func(e event) time.Time { return e.at })

	old := event{"old", base}
	recent := event{"recent", base.Add(10 * time.Minute)}
	log.Record(old)
	log.Record(recent)

	t.Run("Since windows by event time", func(t *testing.T) {
		got := log.Since(base.Add(5 * time.Minute))
		if diff := cmp.Diff([]event{recent}, got, cmp.AllowUnexported(event{})); diff != "" {
			t.Errorf("Since should return only records after the cut (-want +got)\n%s", diff)
		}
	})

	t.Run("Filter selects by predicate", func(t *testing.T) {
		got := log.Filter(func(e event) bool { return e.name == "old" })
		if diff := cmp.Diff([]event{old}, got, cmp.AllowUnexported(event{})); diff != "" {
			t.Errorf("Filter should return matching records (-want +got)\n%s", diff)
		}
	})
}
