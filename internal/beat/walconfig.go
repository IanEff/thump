package beat

import "github.com/ianeff/thump/internal/publish"

// WALConfig is publish.WALConfig — re-exported so a beat's Main pulls its
// WAL-shipping config from the same package NewWALPublisher already does.
// The type itself, and its YAML loader, live in publish (not here) because
// internal/beat stays transport-only — see leaf_test.go's
// TestBeatImportsNoBeat. publish.RunShipper is called directly, without a
// facade: it needs no beat-side state to thread through.
type WALConfig = publish.WALConfig

// LoadWALConfig loads path into a WALConfig — see publish.LoadWALConfig.
func LoadWALConfig(path string) (WALConfig, error) {
	return publish.LoadWALConfig(path)
}
