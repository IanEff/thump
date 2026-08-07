// Package harvest runs one authored scenario against a live rig end to end:
// apply preconditions, inject the fault, wait for a terminal outcome, put
// everything back. Restore runs on every exit path, cancellation included, and
// a scenario declaring no restore is refused when the table loads rather than
// discovered at teardown.
package harvest
