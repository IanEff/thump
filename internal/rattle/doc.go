// Package rattle is the Signal Plane: three pure detectors — burn-rate
// acceleration, sustained burn, multi-signal correlation, and historical-
// envelope breach — OR'd together in Reconciler.Reconcile, one detection per
// pass per SLO. Every window is gated by SignalContract before it's scored
// (stale or excluded-window data is attenuated toward a confidence floor, not
// dropped), and every firing detection is enriched with severity, topology,
// and traffic before handoff. rattle detects; it never decides what should be
// done about a detection, never recomputes another plane's fingerprint, and
// never touches infrastructure — that reasoning belongs to clank, downstream
// of the signal.Detection this package emits.
package rattle
