package subjects_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/subjects"
)

// TestSubjectIndex_For pins the resolution both the evidence tools and the
// change source depend on to make any topology claim at all. The rules are
// authored per rig, so the two ways they can be wrong are both represented
// here: coordinates no rule claims, and coordinates two rules claim equally.
// Both resolve to no subject — a fabricated tag is worse than an absent one,
// because gate.go trusts a tag it can match against the frozen topology.
func TestSubjectIndex_For(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		index  subjects.SubjectIndex
		coords subjects.Coordinates
		want   string
	}{
		"For resolves a namespace-only rule when the query carries no labels": {
			index:  subjects.SubjectIndex{{Subject: "acme-api", Coordinates: subjects.Coordinates{Namespace: "acme"}}},
			coords: subjects.Coordinates{Namespace: "acme"},
			want:   "acme-api",
		},
		"For resolves a labelled rule when the query carries exactly its labels": {
			index:  subjects.SubjectIndex{{Subject: "cart", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}}}},
			coords: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}},
			want:   "cart",
		},
		"For resolves a labelled rule when the query carries extra labels beyond it": {
			index:  subjects.SubjectIndex{{Subject: "cart", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}}}},
			coords: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "cart", "tier": "web"}},
			want:   "cart",
		},
		"For prefers the labelled rule over a namespace-only one covering the same query": {
			index: subjects.SubjectIndex{
				{Subject: "rook-operator", Coordinates: subjects.Coordinates{Namespace: "rook-ceph"}},
				{Subject: "ceph-osd", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}}},
			},
			coords: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}},
			want:   "ceph-osd",
		},
		"For resolves nothing when a namespace-wide query matches only per-workload rules": {
			index: subjects.SubjectIndex{
				{Subject: "cart", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}}},
				{Subject: "checkout", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "checkout"}}},
			},
			coords: subjects.Coordinates{Namespace: "otel-demo"},
			want:   "",
		},
		"For resolves nothing when two equally specific rules both match": {
			index: subjects.SubjectIndex{
				{Subject: "ceph-osd", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}}},
				{Subject: "ceph-cluster", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}}},
			},
			coords: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}},
			want:   "",
		},
		"For resolves nothing when a rule names a label the query does not carry": {
			index:  subjects.SubjectIndex{{Subject: "cart", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}}}},
			coords: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"app": "checkout"}},
			want:   "",
		},
		"For resolves nothing for a namespace no rule claims": {
			index:  subjects.SubjectIndex{{Subject: "acme-api", Coordinates: subjects.Coordinates{Namespace: "acme"}}},
			coords: subjects.Coordinates{Namespace: "rook-ceph"},
			want:   "",
		},
		"For on a nil index resolves nothing rather than panicking": {
			index:  nil,
			coords: subjects.Coordinates{Namespace: "acme"},
			want:   "",
		},

		// A changed Kubernetes object states a kind and a name where a query
		// states labels. The same index answers both, which is the point: a rig
		// declares once what belongs to a topology node.
		"For resolves a changed resource by kind and name when the two vocabularies differ": {
			index:  subjects.SubjectIndex{{Subject: "cephblockpool", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Kind: "CephBlockPool", Name: "replicapool"}}},
			coords: subjects.Coordinates{Namespace: "rook-ceph", Kind: "CephBlockPool", Name: "replicapool"},
			want:   "cephblockpool",
		},
		"For prefers a kind-and-name rule over a namespace-only rule matching the same resource": {
			index: subjects.SubjectIndex{
				{Subject: "ceph-cluster", Coordinates: subjects.Coordinates{Namespace: "rook-ceph"}},
				{Subject: "cephblockpool", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Kind: "CephBlockPool", Name: "replicapool"}},
			},
			coords: subjects.Coordinates{Namespace: "rook-ceph", Kind: "CephBlockPool", Name: "replicapool"},
			want:   "cephblockpool",
		},
		"For distinguishes two resources sharing a name by their kind": {
			index: subjects.SubjectIndex{
				{Subject: "ceph-osd", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Kind: "Deployment", Name: "rook-ceph-osd-0"}},
				{Subject: "ceph-control", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Kind: "Service", Name: "rook-ceph-osd-0"}},
			},
			coords: subjects.Coordinates{Namespace: "rook-ceph", Kind: "Service", Name: "rook-ceph-osd-0"},
			want:   "ceph-control",
		},
		"For resolves nothing for a resource whose name no rule claims": {
			index:  subjects.SubjectIndex{{Subject: "cephblockpool", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Kind: "CephBlockPool", Name: "replicapool"}}},
			coords: subjects.Coordinates{Namespace: "rook-ceph", Kind: "CephFilesystem", Name: "myfs"},
			want:   "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, tc.index.For(tc.coords)); diff != "" {
				t.Error("wrong subject resolved from the coordinates (-want +got)\n", diff)
			}
		})
	}
}
