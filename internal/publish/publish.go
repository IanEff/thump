// Package publish is the outbound half of a beat's Transport: the
// Publisher port and three implementations over it — DirPublisher (the
// offline directory-poll write), JetPublisher (JetStream), and
// WALPublisher (the durability leg — journals every object to a local WAL
// before handing it to another Publisher, so the fact survives a crash
// between "decided" and "delivered"). A beat's network edge composes
// WALPublisher over JetPublisher; the offline edge uses DirPublisher
// alone.
package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// Publisher is the port every Transport publishes boundary objects
// through, parameterized on the type it carries — DirPublisher, JetPublisher,
// and WALPublisher are its only implementations.
type Publisher[T any] interface {
	Publish(ctx context.Context, subject string, obj T) error
}

// DirPublisher is the offline Publisher: it writes obj as YAML into Dir
// instead of a broker, so a beat can run (and be inspected) with no NATS
// at all.
type DirPublisher[T any] struct {
	// Dir is created (mode 0o750) if it doesn't already exist.
	Dir string
	// Name derives the on-disk filename, sans extension from the object
	// being published.
	Name func(T) string
}

// Publish YAML-marshals obj and writes it atomically (temp file + rename)
// under Dir, so a reader never observes a partially written file.
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
