package whir_test

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/whir"
)

func TestLoad_ReadsTheLabCatalog(t *testing.T) {
	raw, err := os.ReadFile("testdata/catalog-info.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := whir.Load(raw)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cat.Edges("ceph-rgw")
	want := []string{"cephobjectstore", "rook-operator"} // refs stripped
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ceph-rgw edges (-want +got):\n%s", diff)
	}
}
