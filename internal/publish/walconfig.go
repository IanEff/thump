package publish

import (
	"errors"
	"time"

	"github.com/ianeff/thump/internal/configfile"
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
	wf, err := configfile.Stage[walConfigFile](path, "wal config file")
	if err != nil {
		return WALConfig{}, err
	}

	r := configfile.Require("wal config file", ErrIncompleteWALConfig)
	out := WALConfig{
		MaxBytes:     r.Int64("maxBytes", wf.MaxBytes),
		MaxAge:       r.Duration("maxAge", wf.MaxAge),
		SyncInterval: r.Duration("syncInterval", wf.SyncInterval),
		ShipInterval: r.Duration("shipInterval", wf.ShipInterval),
	}
	if err := r.Err(); err != nil {
		return WALConfig{}, err
	}
	return out, nil
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
