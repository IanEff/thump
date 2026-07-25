package beat

import (
	"errors"
	"fmt"
	"net/http"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestListenerStopWasClean partitions the listener's terminal errors into the
// one that is expected and everything else. The wrapped-sentinel row is the
// load-bearing one: a future refactor that wraps the ListenAndServe error for
// context must not turn a clean shutdown into a false alarm.
func TestListenerStopWasClean(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		err  error
		want bool
	}{
		"listenerStopWasClean is true for a deliberate shutdown":         {http.ErrServerClosed, true},
		"listenerStopWasClean is true for a wrapped shutdown sentinel":   {fmt.Errorf("serve metrics: %w", http.ErrServerClosed), true},
		"listenerStopWasClean is true for no error at all":               {nil, true},
		"listenerStopWasClean is false when the address is already used": {syscall.EADDRINUSE, false},
		"listenerStopWasClean is false for an unrecognised failure":      {errors.New("dummy failure"), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, listenerStopWasClean(tc.err)); diff != "" {
				t.Error("wrong silence verdict for the listener's terminal error (-want +got)", diff)
			}
		})
	}
}
