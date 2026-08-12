package thump

import (
	"context"

	"github.com/ianeff/thump/internal/forge"
)

// Forge is the GitOps source of record a maintenanceRelease contract writes
// to. Its method set matches actuate.Forge exactly, so one constructed
// client satisfies the actuator's seam and ReleaseProbe both, without this
// package handing cmd/thump an actuate type to name — the same reasoning as
// ReleaseProbe's own doc comment.
type Forge interface {
	Read(ctx context.Context, path string) ([]byte, error)
	Cut(ctx context.Context, rel forge.Release) (url string, err error)
	Withdraw(ctx context.Context, key string) (accepted bool, err error)
}
