package rattle

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/internal/signal"
	"sigs.k8s.io/yaml"
)

type DetectionSink interface {
	Deliver(d signal.Detection) error
}

type DirSink struct {
	Dir string
}

func (s *DirSink) Deliver(d signal.Detection) error {
	out, err := yaml.Marshal(d)
	if err != nil {
		return fmt.Errorf("rattle: marshal detection: %w", err)
	}
	name := fmt.Sprintf("%s-%d.yaml", d.Fingerprint, time.Now().UnixNano())
	path := filepath.Join(s.Dir, name)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("dir sink: write %s: %w", path, err)
	}
	return nil
}
