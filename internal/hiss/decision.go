package hiss

import "github.com/ianeff/thump/internal/decision"

type (
	Decision = decision.Decision
	Verdict  = decision.Verdict
	Band     = decision.Band
)

const (
	VerdictApproved = decision.VerdictApproved
	VerdictEscalate = decision.VerdictEscalate
	VerdictRejected = decision.VerdictRejected

	BandObserve       = decision.BandObserve
	BandActReversible = decision.BandActReversible
	BandActDisruptive = decision.BandActDisruptive
)
