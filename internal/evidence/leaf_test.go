package evidence_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestEvidenceIsALeafPackage pins that internal/evidence stays a leaf: the
// four read-only reason.Tool/reason.ChangeSource adaptors depend only on
// client-go and this repo's own small leaves — never NATS, OTel, Prometheus,
// or an LLM SDK.
func TestEvidenceIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib,
		"github.com/ianeff/thump/api/v1/proposal",
		"github.com/ianeff/thump/api/v1/signal",
		"github.com/ianeff/thump/internal/httpx",
		"github.com/ianeff/thump/internal/reason",
		"github.com/ianeff/thump/internal/schema",
		"github.com/ianeff/thump/internal/subjects",
		"sigs.k8s.io/yaml",
		"k8s.io/api/apps/v1",
		"k8s.io/api/core/v1",
		"k8s.io/apimachinery/pkg/apis/meta/v1",
		"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured",
		"k8s.io/apimachinery/pkg/labels",
		"k8s.io/apimachinery/pkg/runtime/schema",
		"k8s.io/client-go/dynamic",
		"k8s.io/client-go/kubernetes",
	)
}
