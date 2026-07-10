// Package tracing mints the deterministic trace identity every beat derives
// independently from a signal.Detection.Fingerprint — never passed hand to
// hand, so rattle's root span and clank/hiss/thump's child spans land on the
// same Tempo trace without any beat trusting another beat's wire value.
package tracing

import (
	"context"
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

// RootContext seeds ctx with a remote SpanContext carrying
// TraceIDFromFingerprint(fingerprint) — the one line every incident-minting
// call site needs, so rattle's runLoop (the only production caller) doesn't
// hand-roll the SpanContext/Remote/ContextWithRemoteSpanContext trio itself.
// Marking the seed Remote is what tells the SDK "inherit this trace ID, don't
// treat SpanID{1} as a real span": a tracer.Start on the returned context
// mints a genuine, exportable child span rather than reusing that fabricated
// SpanID as if it were already live.
func RootContext(ctx context.Context, fingerprint string) context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    TraceIDFromFingerprint(fingerprint),
		SpanID:     trace.SpanID{1},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithRemoteSpanContext(ctx, sc)
}
