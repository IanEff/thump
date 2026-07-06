package rattle_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/wire"
)

func TestRattlePublisher_JournalsAndPublishes(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx := context.Background()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	walDir := t.TempDir()
	w := &publish.WAL{Dir: walDir, Beat: "rattle", Subject: "thump.detections"}
	defer func() { _ = w.Close(ctx) }()
	pub := &publish.WALPublisher[signal.Detection]{
		WAL:  w,
		Next: publish.NewJetPublisher[signal.Detection](js),
	}

	in := signal.Detection{Fingerprint: "slo_burn:ceph-rgw", ServiceTier: "tier-1"}
	if err := pub.Publish(ctx, "thump.detections", in); err != nil {
		t.Fatal("publish:", err)
	}

	// on the wire
	stream, _ := js.Stream(ctx, "THUMP")
	raw, err := stream.GetLastMsgForSubject(ctx, "thump.detections")
	if err != nil {
		t.Fatal("not on the stream:", err)
	}
	var got signal.Detection
	if err := wire.Unmarshal(raw.Data, &got); err != nil {
		t.Fatal("wire bytes didn't decode:", err)
	}
	if diff := cmp.Diff(in, got); diff != "" {
		t.Error("detection didn't survive publish (-want +got)", diff)
	}

	// and in the WAL: exactly one sealed-or-active line for this beat/subject
	if n := walLineCount(t, filepath.Join(walDir, "rattle", "thump.detections")); n != 1 {
		t.Errorf("WAL journaled %d lines, want 1", n)
	}
}

// walLineCount counts non-empty lines across every segment (active.jsonl and
// any sealed *-*.jsonl) in a beat/subject's WAL directory.
func walLineCount(t *testing.T, dirpath string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dirpath, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob %s: %v", dirpath, err)
	}
	n := 0
	for _, m := range matches {
		raw, err := os.ReadFile(m) //nolint:gosec
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
	}
	return n
}
