package hiss

import (
	"time"

	"github.com/ianeff/clank/internal/proposal"
)

type Window struct {
	Name       string
	Start, End time.Time
}

type Policy struct {
	Version         string
	Floors          map[string]map[proposal.FailureClass]float64
	MaxBand         map[string]Band
	FreezeWindows   []Window
	RequireReversal bool
}
