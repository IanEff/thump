package publish

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// ErrIncompleteWALConfig means a WAL config file omitted a term rather than
// setting it.
var ErrIncompleteWALConfig = errors.New("wal config file is missing one or more required terms")

// WALConfig bounds every beat's WAL sealing and shipping cadence — one
// surface shared identically across beats, since WAL's segment sizing and
// beat.RunShipper's poll interval are common code, not per-beat tuning.
type WALConfig struct {
	// MaxBytes seals a WAL's active segment once it reaches this size.
	MaxBytes int64

	// MaxAge seals a WAL's active segment once it's been open this long.
	MaxAge time.Duration

	// SyncInterval is a WAL's background fsync cadence.
	SyncInterval time.Duration

	// ShipInterval is RunShipper's poll cadence for sealed, unshipped
	// segments.
	ShipInterval time.Duration
}

// walConfigFile stages a wal.yaml before validation — every field a
// pointer, the three durations staged as *string, for the same reasons
// clank's weightsFile does (see clank/weights.go's doc comment).
type walConfigFile struct {
	MaxBytes     *int64  `json:"maxBytes"`
	MaxAge       *string `json:"maxAge"`
	SyncInterval *string `json:"syncInterval"`
	ShipInterval *string `json:"shipInterval"`
}

// LoadWALConfig reads path as a YAML file and validates it into a
// WALConfig — fail at load, never at first use, same posture as every
// other config surface in this tree.
func LoadWALConfig(path string) (WALConfig, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config path, not user input
	if err != nil {
		return WALConfig{}, fmt.Errorf("read wal config file: %w", err)
	}
	var wf walConfigFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return WALConfig{}, fmt.Errorf("parse wal config file: %w", err)
	}

	missing := []string{}
	need := func(name string, present bool) {
		if !present {
			missing = append(missing, name)
		}
	}
	need("maxBytes", wf.MaxBytes != nil)
	need("maxAge", wf.MaxAge != nil)
	need("syncInterval", wf.SyncInterval != nil)
	need("shipInterval", wf.ShipInterval != nil)

	if len(missing) > 0 {
		return WALConfig{}, fmt.Errorf("%w: %s", ErrIncompleteWALConfig, strings.Join(missing, ", "))
	}

	maxAge, err := time.ParseDuration(*wf.MaxAge)
	if err != nil {
		return WALConfig{}, fmt.Errorf("wal config file maxAge: %w", err)
	}
	syncInterval, err := time.ParseDuration(*wf.SyncInterval)
	if err != nil {
		return WALConfig{}, fmt.Errorf("wal config file syncInterval: %w", err)
	}
	shipInterval, err := time.ParseDuration(*wf.ShipInterval)
	if err != nil {
		return WALConfig{}, fmt.Errorf("wal config file shipInterval: %w", err)
	}

	return WALConfig{
		MaxBytes:     *wf.MaxBytes,
		MaxAge:       maxAge,
		SyncInterval: syncInterval,
		ShipInterval: shipInterval,
	}, nil
}

// DefaultWALConfig is the WAL sealing and shipping cadence every beat
// wired before extraction — 64MiB segments, sealed by age at 10 minutes,
// background-synced every 5 seconds, shipped every 30 seconds.
func DefaultWALConfig() WALConfig {
	return WALConfig{
		MaxBytes:     64 * 1024 * 1024,
		MaxAge:       10 * time.Minute,
		SyncInterval: 5 * time.Second,
		ShipInterval: 30 * time.Second,
	}
}
