package publish_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/s3test"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeSink is the in-memory SegmentSink double: it records every key it was
// asked to store, and — if errOn is set — fails a Put for that one key so
// tests can assert Ship's retry-safety without a real network failure.
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

func TestWAL_ShipUploadsASealedSegmentAndRemovesItLocally(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections", MaxBytes: 1}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	ctx := context.Background()

	if err := w.Append(ctx, signal.Detection{Fingerprint: "fp-1"}); err != nil {
		t.Fatal(err)
	}
	sealed, err := w.SealedSegments()
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 1 {
		t.Fatalf("got %d sealed segments before Ship, want 1", len(sealed))
	}
	wantKey := "rattle/thump.detections/" + filepath.Base(sealed[0])

	sink := newFakeSink()
	if err := w.Ship(ctx, sink); err != nil {
		t.Fatal(err)
	}

	if _, ok := sink.puts[wantKey]; !ok {
		t.Errorf("sink never received %q; got keys %v", wantKey, sink.puts)
	}
	if _, err := os.Stat(sealed[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sealed segment %s should be removed after a successful ship, stat err = %v", sealed[0], err)
	}
}

func TestWAL_ShipNeverTouchesTheActiveSegment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections"}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	ctx := context.Background()

	if err := w.Append(ctx, signal.Detection{Fingerprint: "fp-1"}); err != nil {
		t.Fatal(err)
	}

	sink := newFakeSink()
	if err := w.Ship(ctx, sink); err != nil {
		t.Fatal(err)
	}

	if len(sink.puts) != 0 {
		t.Errorf("Ship uploaded the active segment, want it left alone: %v", sink.puts)
	}
	if _, err := os.Stat(filepath.Join(dir, "rattle", "thump.detections", "active.jsonl")); err != nil {
		t.Errorf("active segment should still be on disk: %v", err)
	}
}

func TestWAL_ShipIsANoOpOnceASegmentIsAlreadyShipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections", MaxBytes: 1}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	ctx := context.Background()

	if err := w.Append(ctx, signal.Detection{Fingerprint: "fp-1"}); err != nil {
		t.Fatal(err)
	}
	sink := newFakeSink()
	if err := w.Ship(ctx, sink); err != nil {
		t.Fatal(err)
	}
	firstShipCount := len(sink.puts)

	if err := w.Ship(ctx, sink); err != nil {
		t.Fatal(err)
	}

	if len(sink.puts) != firstShipCount {
		t.Errorf("a second Ship call re-uploaded an already-shipped segment: had %d puts, now %d", firstShipCount, len(sink.puts))
	}
}

func TestWAL_ShipLeavesASegmentInPlaceWhenTheSinkFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections", MaxBytes: 1}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	ctx := context.Background()

	if err := w.Append(ctx, signal.Detection{Fingerprint: "fp-1"}); err != nil {
		t.Fatal(err)
	}
	sealed, err := w.SealedSegments()
	if err != nil {
		t.Fatal(err)
	}
	sink := newFakeSink()
	sink.errOn = "rattle/thump.detections/" + filepath.Base(sealed[0])

	if err := w.Ship(ctx, sink); err == nil {
		t.Fatal("Ship() error = nil, want non-nil when the sink refuses the put")
	}

	if _, err := os.Stat(sealed[0]); err != nil {
		t.Errorf("a segment that failed to upload must stay on disk for retry, stat err = %v", err)
	}
}

func TestS3SegmentSink_PutStoresBytesRetrievableFromS3(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, bucket := s3test.New(t)
	sink := publish.NewS3SegmentSink(client, bucket)

	want := []byte(`{"Fingerprint":"fp-1"}` + "\n")
	if err := sink.Put(ctx, "rattle/thump.detections/seg-1.jsonl", bytes.NewReader(want)); err != nil {
		t.Fatal(err)
	}

	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("rattle/thump.detections/seg-1.jsonl"),
	})
	if err != nil {
		t.Fatalf("get uploaded segment: %v", err)
	}
	defer func() { _ = out.Body.Close() }()

	got, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("uploaded segment content = %q, want %q", got, want)
	}
}
