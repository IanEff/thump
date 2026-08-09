package evidence_test

import (
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/reason"
)

// TestToolSpecs_NeverShipARigsVocabularyToADomainThatNeverAuthoredIt loads
// acme's evidence-queries.yaml — deliberately not ceph, otel, flagd or cart —
// and fails if either tool's Description names a word only those rigs
// authored, pinning that a tool's advertised vocabulary comes from the
// SubjectIndex it's given, never a literal baked into the binary.
func TestToolSpecs_NeverShipARigsVocabularyToADomainThatNeverAuthoredIt(t *testing.T) {
	t.Parallel()

	cfg, err := evidence.LoadEvidenceConfig("../../test/onboarding/testdata/acme/whir/evidence-queries.yaml")
	if err != nil {
		t.Fatal(err)
	}

	tools := map[string]reason.Tool{
		"loki Spec names only what acme authored": &evidence.LokiTool{Subjects: cfg.Index},
		"kube Spec names only what acme authored": &evidence.KubeTool{Subjects: cfg.Index},
	}
	banned := []string{"ceph", "rook", "cart", "flagd", "otel", "osd", "rgw"}

	for name, tool := range tools {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			spec := tool.Spec()
			desc := strings.ToLower(spec.Description)
			for _, word := range banned {
				if strings.Contains(desc, word) {
					t.Errorf("tool %q ships %q into the model prompt on a domain that never authored it: %q",
						spec.Name, word, spec.Description)
				}
			}
		})
	}
}
