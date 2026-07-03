package clank

import "errors"

var (
	ErrUnauditableOutcome = errors.New("click: outcome fails its audit invariant")
	ErrIncoherentOutcome  = errors.New("click: outcome mode and result disagree")
)
