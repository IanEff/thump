package actuate

// NewWith exposes the fake-CommandRunner constructor to the external test
// package without widening the production surface.
func NewWith(run CommandRunner) *Runner { return newWith(run) }
