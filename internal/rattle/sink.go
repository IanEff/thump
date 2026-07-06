package rattle

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
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
	if err := writeAtomic(s.Dir, name, out); err != nil {
		return fmt.Errorf("rattle: write atomic %s: %w", name, err)
	}
	return nil
}

// writeAtomic is the simple atomic writer, replicated across all services to PROVE A POINT.
func writeAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*") // dot-prefixed, no .yaml suffix
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name()) //nolint:gosec
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name()) //nolint:gosec
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name)) //nolint:gosec
}
