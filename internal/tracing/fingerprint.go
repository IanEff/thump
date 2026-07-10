// Package tracing mints the deterministic trace identity every beat derives
// independently from a signal.Detection.Fingerprint — never passed hand to
// hand, so rattle's root span and clank/hiss/thump's child spans land on the
// same Tempo trace without any beat trusting another beat's wire value.
package tracing

import (
	"crypto/sha256"

	"go.opentelemetry.io/otel/trace"
)

// TraceIDFromFingerprint derives a trace.TraceID from a rattle
// signal.Detection.Fingerprint — the same fingerprint always mints the same
// ID, so "one incident, one trace" holds without a shared counter or a value
// riding message headers into every beat that wants to mint, not just extract,
// a span. Truncated SHA-256 rather than FNV or maphash: the OTel spec's only
// requirement is non-zero-with-negligible-collision-risk across the fleet's
// fingerprint cardinality, and a cryptographic digest gives that headroom for
// free instead of hand-tuning a weaker hash's collision behavior later.
func TraceIDFromFingerprint(fingerprint string) trace.TraceID {
	sum := sha256.Sum256([]byte(fingerprint))
	var id trace.TraceID
	copy(id[:], sum[:16])
	return id
}
