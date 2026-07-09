// Package publishtest provides the in-memory Publisher double beats reach for
// in tests. It lives in its own non-test package (rather than each beat's
// _test.go) so the same recording fake serves clank, rattle, and the
// integration harness instead of being reimplemented in each.
package publishtest

import "context"

// CapturePublisher records every object published, so a test can assert on
// Delivered. It satisfies publish.Publisher[T].
type CapturePublisher[T any] struct {
	// Delivered holds every object passed to Publish, in call order.
	Delivered []T
}

// Publish appends obj to Delivered and always succeeds — there is no
// failure path to simulate a network Publisher's errors, this double
// exists only to record what was sent.
func (c *CapturePublisher[T]) Publish(_ context.Context, _ string, obj T) error {
	c.Delivered = append(c.Delivered, obj)
	return nil
}
