package clank_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/publish"
)

func TestDirPublisher_WritesOneFilePerProposalSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pub := &publish.DirPublisher[proposal.Set]{Dir: dir, Name: func(ps proposal.Set) string { return ps.SignalRef }}

	if err := pub.Publish(context.Background(), "thump.propsals", proposal.Set{
		Name: "n", SignalRef: "slo_burn:ceph-rgw",
	}); err != nil {
		t.Fatal(err)
	}
	// named by fingerprint so a re-proposal of the same incident overwrites,
	// never piles up — the file inbox inherits the ledger's dedup intent.
	if _, err := os.Stat(filepath.Join(dir, "slo_burn:ceph-rgw.yaml")); err != nil {
		t.Errorf("DirSink must write <fingerprint>.yaml: %v", err)
	}
}
