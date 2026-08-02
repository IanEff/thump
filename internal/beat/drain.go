package beat

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// DrainDir globs every *.yaml file in dir, unmarshals each into a T, and
// hands it to handle along with the file's path. A file that fails to
// unmarshal is quarantined and skipped — poison never blocks the rest of the
// pass. Every other disposition (processed, stalled, unmatched, ...) is
// handle's call, made by returning a nil error and disposing of path itself;
// DrainDir stops and returns the first non-nil error handle produces, since
// only handle knows whether that failure is safe to skip past.
func DrainDir[T any](dir, prefix string, handle func(path string, v T) error) error {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("%s: list inbox: %w", prefix, err)
	}

	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec // G304: path came from filepath.Glob under dir, not user input
		if err != nil {
			return fmt.Errorf("%s: read %s: %w", prefix, path, err)
		}

		var v T
		if err := yaml.Unmarshal(raw, &v); err != nil {
			if qErr := Disposition(dir, path, "quarantine"); qErr != nil {
				return fmt.Errorf("%s: quarantine %s: %w", prefix, path, qErr)
			}
			continue
		}

		if err := handle(path, v); err != nil {
			return err
		}
	}
	return nil
}

// Disposition moves path into dir's sub subdirectory, creating it if it
// doesn't exist yet — the terminal step of an inbox file's lifecycle,
// whatever name a caller gives sub ("processed", "quarantine", "stalled",
// "unmatched", "skipped", ...).
func Disposition(dir, path, sub string) error {
	target := filepath.Join(dir, sub)
	if err := os.MkdirAll(target, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(target, filepath.Base(path)))
}
