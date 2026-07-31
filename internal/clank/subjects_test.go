package clank_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
)

// TestSubjectIndex_For pins the resolution the log and cluster tools depend
// on to make any topology claim at all. The rules are authored per rig, so
// the two ways they can be wrong are both represented here: a query no rule
// claims, and a query two rules claim equally. Both resolve to no subject —
// a fabricated tag is worse than an absent one, because gate.go trusts a tag
// it can match against the frozen topology.
func TestSubjectIndex_For(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		index     clank.SubjectIndex
		namespace string
		labels    map[string]string
		want      string
	}{
		"For resolves a namespace-only rule when the query carries no labels": {
			index:     clank.SubjectIndex{{Subject: "acme-api", Namespace: "acme"}},
			namespace: "acme",
			want:      "acme-api",
		},
		"For resolves a labelled rule when the query carries exactly its labels": {
			index:     clank.SubjectIndex{{Subject: "cart", Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}}},
			namespace: "otel-demo",
			labels:    map[string]string{"app": "cart"},
			want:      "cart",
		},
		"For resolves a labelled rule when the query carries extra labels beyond it": {
			index:     clank.SubjectIndex{{Subject: "cart", Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}}},
			namespace: "otel-demo",
			labels:    map[string]string{"app": "cart", "tier": "web"},
			want:      "cart",
		},
		"For prefers the labelled rule over a namespace-only one covering the same query": {
			index: clank.SubjectIndex{
				{Subject: "rook-operator", Namespace: "rook-ceph"},
				{Subject: "ceph-osd", Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}},
			},
			namespace: "rook-ceph",
			labels:    map[string]string{"app": "rook-ceph-osd"},
			want:      "ceph-osd",
		},
		"For resolves nothing when a namespace-wide query matches only per-workload rules": {
			index: clank.SubjectIndex{
				{Subject: "cart", Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}},
				{Subject: "checkout", Namespace: "otel-demo", Labels: map[string]string{"app": "checkout"}},
			},
			namespace: "otel-demo",
			want:      "",
		},
		"For resolves nothing when two equally specific rules both match": {
			index: clank.SubjectIndex{
				{Subject: "ceph-osd", Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}},
				{Subject: "ceph-cluster", Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}},
			},
			namespace: "rook-ceph",
			labels:    map[string]string{"app": "rook-ceph-osd"},
			want:      "",
		},
		"For resolves nothing when a rule names a label the query does not carry": {
			index:     clank.SubjectIndex{{Subject: "cart", Namespace: "otel-demo", Labels: map[string]string{"app": "cart"}}},
			namespace: "otel-demo",
			labels:    map[string]string{"app": "checkout"},
			want:      "",
		},
		"For resolves nothing for a namespace no rule claims": {
			index:     clank.SubjectIndex{{Subject: "acme-api", Namespace: "acme"}},
			namespace: "rook-ceph",
			want:      "",
		},
		"For on a nil index resolves nothing rather than panicking": {
			index:     nil,
			namespace: "acme",
			want:      "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, tc.index.For(tc.namespace, tc.labels)); diff != "" {
				t.Error("wrong subject resolved from the query's coordinates (-want +got)\n", diff)
			}
		})
	}
}
