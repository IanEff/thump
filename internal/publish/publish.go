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
	// Name derives the on-disk filename, sans extension from the object
	// being published.
	Name func(T) string
}

func (p *DirPublisher[T]) Publish(_ context.Context, subject string, obj T) error {
	if err := os.MkdirAll(p.Dir, 0o750); err != nil {
		return fmt.Errorf("dir publisher: mkdir %s: %w", p.Dir, err)
	}

	out, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("dir publisher: marshal %s: %w", subject, err)
	}
	key := fmt.Sprintf("%s-%d", subject, time.Now().UnixNano())
	if p.Name != nil {
		if n := p.Name(obj); n != "" {
			key = n
		}
	}
	return writeAtomic(p.Dir, key+".yaml", out)
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
