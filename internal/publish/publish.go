package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// Publisher is the port for the Transport, parameterized on the boundary type it produces.
type Publisher[T any] interface {
	Publish(ctx context.Context, subject string, obj T) error
}

// DirPublisher is the directory write implementation of Publisher, the port for the Transport.
type DirPublisher[T any] struct {
	Dir string
}

func (p *DirPublisher[T]) Publish(_ context.Context, subject string, obj T) error {
	out, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("dir publisher: marshal %s: %w", subject, err)
	}
	name := fmt.Sprintf("%s-%d.yaml", subject, time.Now().UnixNano())
	return writeAtomic(p.Dir, name, out)
}

func writeAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
