package beat_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/sealbox"
)

// fakeSink is the in-memory publish.SegmentSink double proven in
// internal/publish/shipper_test.go — reproduced here rather than shared
// because it's a handful of lines and internal/beat must not import a
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
	sink := &beat.EncryptingSink{Inner: inner, Key: key}
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

func TestEncryptingSink_PutPropagatesAnInnerSinkError(t *testing.T) {
	t.Parallel()
	inner := newFakeSink()
	inner.errOn = "hiss/thump.decisions/seg-1"
	sink := &beat.EncryptingSink{Inner: inner, Key: newTestSealKey(t)}

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
