// Package bootstrap is the one-shot Job that creates or updates the shared
// JetStream stream and its durable consumers — the only caller of
// broker.ConnectAndEnsure, so it is the only identity in nats.conf that
// needs $JS.API.> and every beat's own cert can hold a narrower grant.
package bootstrap

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/tlsx"
)

// timeout bounds the connect-and-ensure call so a Job with an unreachable
// broker fails fast and lets Kubernetes' backoffLimit retry it, rather than
// hanging until the pod's own activeDeadlineSeconds kills it.
const timeout = 30 * time.Second

// Main connects to NATS, ensures the shared topology exists, and exits — a
// beat's own broker.Connect never calls EnsureTopology, so this is the only
// place in the deployment that does. It returns a process exit code rather
// than calling os.Exit, so the whole run stays testable.
func Main(_ []string, stderr io.Writer) int {
	cfg, err := config.LoadBootstrap()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// No hooks: this is a Job that runs to completion under a timeout, so a
	// lost connection surfaces as a failed exit code, not a readiness flip.
	_, closeNC, err := broker.ConnectAndEnsure(ctx, cfg.NATSURL, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	}, broker.Hooks{})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	closeNC()
	return 0
}
