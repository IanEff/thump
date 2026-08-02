package beat

import "github.com/ianeff/thump/internal/publish"

// WALConfig is publish.WALConfig — re-exported so a beat's Main pulls every
// composition-root dependency from one package, the same shape
// NewWALPublisher and RunShipper already give it. The type itself, and its
// YAML loader, live in publish (not here) because internal/beat stays
// transport-only — see leaf_test.go's TestBeatImportsNoBeat.
type WALConfig = publish.WALConfig

// LoadWALConfig loads path into a WALConfig — see publish.LoadWALConfig.
func LoadWALConfig(path string) (WALConfig, error) {
	return publish.LoadWALConfig(path)
}
