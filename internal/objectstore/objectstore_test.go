package objectstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/sealbox"
)

// fakeSink is the in-memory publish.SegmentSink double proven in
// internal/publish/shipper_test.go — reproduced here rather than shared
// because it's a handful of lines and internal/objectstore must not import a
// _test.go helper from another package.
type fakeSink struct {
	puts  map[string][]byte
	errOn string
}

func newFakeSink() *fakeSink {
	return &fakeSink{puts: make(map[string][]byte)}
}

func (f *fakeSink) Put(_ context.Context, key string, r io.Reader) error {
	if key == f.errOn {
		return errors.New("fake sink: put refused")
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.puts[key] = b
	return nil
}

func TestEncryptingSink_PutStoresSealedBytesAtTheInnerSink(t *testing.T) {
	t.Parallel()
	inner := newFakeSink()
	key := newTestSealKey(t)
	sink := &objectstore.EncryptingSink{Inner: inner, Key: key}
	plaintext := []byte(`{"fingerprint":"fp-1"}`)

	if err := sink.Put(context.Background(), "rattle/thump.detections/seg-1", bytes.NewReader(plaintext)); err != nil {
		t.Fatal(err)
	}

	got, ok := inner.puts["rattle/thump.detections/seg-1"]
	if !ok {
		t.Fatal("EncryptingSink.Put never reached the inner sink")
	}
	if bytes.Equal(got, plaintext) {
		t.Error("inner sink received the plaintext segment, want sealed bytes")
	}

	opened, err := key.Open(got)
	if err != nil {
		t.Fatalf("Key.Open on the sealed bytes: %v", err)
	}
	if diff := cmp.Diff(plaintext, opened); diff != "" {
		t.Error("sealed bytes didn't recover the original plaintext", diff)
	}
}

// TestUnsealSegment_RecoversTheLinesEncryptingSinkShipped is a round trip on
// purpose: the two halves are the only pair in the tree, and pinning either
// against a hand-built fixture would pin the fixture rather than the pairing.
// A segment nobody can read back is an audit trail in name only.
func TestUnsealSegment_RecoversTheLinesEncryptingSinkShipped(t *testing.T) {
	t.Parallel()
	inner := newFakeSink()
	key := newTestSealKey(t)
	sink := &objectstore.EncryptingSink{Inner: inner, Key: key}
	// One segment holds many boundary objects, so the trailing newline the WAL
	// writes must not decode as a final empty line.
	segment := []byte("{\"name\":\"set-1\"}\n{\"name\":\"set-2\"}\n")

	if err := sink.Put(context.Background(), "clank/thump.proposals/seg-1", bytes.NewReader(segment)); err != nil {
		t.Fatal(err)
	}

	got, err := objectstore.UnsealSegment(key, bytes.NewReader(inner.puts["clank/thump.proposals/seg-1"]))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{[]byte(`{"name":"set-1"}`), []byte(`{"name":"set-2"}`)}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("unsealing a shipped segment didn't recover its lines (-want +got)\n", diff)
	}
}

// TestUnsealSegment_RefusesTheWrongKeyRatherThanReturningGarbage pins the
// failure an operator actually hits: kubectl returns the Secret's value
// encoded on top of THUMP_SEAL_KEY's own encoding, so the key that reaches
// this function is the right length and the wrong bytes.
func TestUnsealSegment_RefusesTheWrongKeyRatherThanReturningGarbage(t *testing.T) {
	t.Parallel()
	inner := newFakeSink()
	sink := &objectstore.EncryptingSink{Inner: inner, Key: newTestSealKey(t)}
	if err := sink.Put(context.Background(), "clank/thump.proposals/seg-1", bytes.NewReader([]byte("{}\n"))); err != nil {
		t.Fatal(err)
	}

	var other sealbox.Key
	other[0] = 1
	if _, err := objectstore.UnsealSegment(other, bytes.NewReader(inner.puts["clank/thump.proposals/seg-1"])); err == nil {
		t.Error("unsealing with the wrong key must fail, not return whatever the cipher produced")
	}
}

func TestEncryptingSink_PutPropagatesAnInnerSinkError(t *testing.T) {
	t.Parallel()
	inner := newFakeSink()
	inner.errOn = "hiss/thump.decisions/seg-1"
	sink := &objectstore.EncryptingSink{Inner: inner, Key: newTestSealKey(t)}

	err := sink.Put(context.Background(), "hiss/thump.decisions/seg-1", bytes.NewReader([]byte("payload")))
	if err == nil {
		t.Error("Put returned nil error despite the inner sink refusing the write")
	}
}

func newTestSealKey(t *testing.T) sealbox.Key {
	t.Helper()
	var k sealbox.Key
	for i := range k {
		k[i] = byte(i)
	}
	return k
}
