package rca

import (
	"path/filepath"
	"runtime"
)

// repoRoot resolves relative to this source file, not the process's working
// directory — this package runs under both `go test` (cwd = internal/rca)
// and `go run`/`task rca` (cwd = the repo root), and a path relative to one
// breaks under the other.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// configPath joins elem onto the repo's config/ directory.
func configPath(elem ...string) string {
	return filepath.Join(append([]string{repoRoot(), "config"}, elem...)...)
}

// detectionPath resolves a fixture filename under clank's detection testdata.
func detectionPath(name string) string {
	return filepath.Join(repoRoot(), "internal", "clank", "testdata", "detections", name)
}
