package beat

import "time"

// Clock returns fn, or time.Now when fn is nil — every beat's overridable
// Now field defaults this way, so a frozen test clock never needs its call
// site to nil-check the override itself.
func Clock(fn func() time.Time) func() time.Time {
	if fn != nil {
		return fn
	}
	return time.Now
}
