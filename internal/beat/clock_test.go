package beat_test

import (
	"testing"
	"time"

	"github.com/ianeff/thump/internal/beat"
)

func TestClock_ANilFnDefaultsToTimeNow(t *testing.T) {
	t.Parallel()
	before := time.Now()

	got := beat.Clock(nil)()

	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("a nil fn must default to the real clock, got %v outside [%v, now]", got, before)
	}
}

func TestClock_ANonNilFnPassesThrough(t *testing.T) {
	t.Parallel()
	frozen := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	got := beat.Clock(func() time.Time { return frozen })()

	if !got.Equal(frozen) {
		t.Errorf("a non-nil fn must be returned unchanged, got %v want %v", got, frozen)
	}
}
