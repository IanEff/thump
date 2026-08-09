# Step 4 — Stop shipping Ceph into every model prompt

## Context

`internal/evidence/loki_tool.go` and `kube_tool.go` hardcode Ceph label keys
(`ceph_daemon_id`, `ceph_daemon_type`, …) and a `{"app": "cart"}` example
straight into the tool descriptions the model reads on *every* rig — acme,
otel-demo, anyone else who onboards. This is the concrete instance of
"looking like cringe homelab bullshit": a generalist engine that leaks one
operator's topology into its own prompt text, unconditionally. Both tools
already hold a `subjects.SubjectIndex` (`Subjects` field) built from that
rig's `whir/evidence-queries.yaml` — the keys a rig actually authored are
already in hand when `Spec()` runs. The fix is mechanical: derive from the
index instead of a literal. `rca.go` has the same shape bug one level up —
`-rig` defaults to `thump-test` and a Ceph pod is hand-built into the
composition root, so grading silently assumes Ceph unless told otherwise.

Per the phase doc: **the test is the deliverable.** 4c is written and run
*before* 4a/4b land, confirmed red, so the fix is proven rather than assumed.

Four sub-steps, in this order: 4c (red) → 4a → 4b → 4c (green) → 4d.

## 4c — the guard: `internal/evidence/toolspec_test.go` (new)

Loads `test/onboarding/testdata/acme/whir/evidence-queries.yaml` — acme
declares no labeled subject rule at all (only a bare `namespace: acme`), so
this fixture is the sharpest possible probe: any Ceph/otel/cart vocabulary
showing up in a Spec built from it can only be a hardcoded literal, never
something derived from the index.

```go
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
		"kube Spec names only what acme authored":  &evidence.KubeTool{Subjects: cfg.Index},
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
```

**Run it before touching loki_tool.go / kube_tool.go.** Expected red on both
subtests — `loki` on `ceph_daemon_id`/`ceph_daemon_type`, `kube` on `cart`.
Paste the actual failure text into the vault notes per the phase doc's
verification section.

## 4a — `internal/evidence/loki_tool.go`

Add the derivation to `subjects.SubjectIndex` itself (`internal/subjects/subjects.go`)
rather than duplicating dedupe/sort in the tool — it's the one place both
tools already depend on, and `loki_tool.go` already has this exact
sort-and-dedupe shape in `buildLogQL`/`lokiRef` for the same reason: keep it
next to `For`, not scattered per caller.

```go
// LabelKeys returns every label key declared across x's rules, sorted and
// deduplicated — the vocabulary a tool description can advertise without
// naming a key no rig authored. Nil when no rule declares any.
func (x SubjectIndex) LabelKeys() []string {
	seen := make(map[string]struct{})
	for _, rule := range x {
		for k := range rule.Labels {
			seen[k] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
```

(needs `"sort"` added to `subjects.go`'s imports)

`loki_tool.go`'s `Spec()`:

```go
func (l *LokiTool) Spec() reason.ToolSpec {
	desc := "read-only log query. namespace is required."
	if keys := l.Subjects.LabelKeys(); len(keys) > 0 {
		desc += " Known label keys: " + strings.Join(keys, ", ") + "."
	}
	desc += " query is an optional line-filter substring (NOT LogQL syntax)" +
		" — do not pass raw LogQL, it will be escaped as a literal."
	return reason.ToolSpec{
		Name:        "loki",
		Description: desc,
		InputSchema: schema.Of[lokiInput](),
	}
}
```

`strings` is already imported in `loki_tool.go`. No caller change —
`Subjects` is already populated at construction (`clank.go:285`).

## 4b — `internal/evidence/kube_tool.go`

Same method family, one more method on `SubjectIndex` since "one real pair"
needs a deterministic pick — first rule declaring any label, smallest key by
sort (maps don't iterate in a stable order):

```go
// ExampleLabel returns one label key/value pair from the first rule in x
// that declares any — a tool description's worked example, drawn from what
// the rig actually authored rather than invented. ok is false when no rule
// declares a label.
func (x SubjectIndex) ExampleLabel() (key, value string, ok bool) {
	for _, rule := range x {
		if len(rule.Labels) == 0 {
			continue
		}
		keys := make([]string, 0, len(rule.Labels))
		for k := range rule.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys[0], rule.Labels[keys[0]], true
	}
	return "", "", false
}
```

`kube_tool.go`'s `Spec()`:

```go
func (k *KubeTool) Spec() reason.ToolSpec {
	desc := "read-only kubernetes resource query (supports resource: 'pods')." +
		" selector is an optional map of label equality pairs"
	if key, value, ok := k.Subjects.ExampleLabel(); ok {
		desc += fmt.Sprintf(", e.g. {%q: %q}", key, value)
	}
	desc += " — narrow to one workload with it; an unnarrowed namespace query" +
		" spans every workload in it and cannot be evidence about any one of them."
	return reason.ToolSpec{
		Name:        "kube",
		Description: desc,
		InputSchema: schema.Of[kubeInput](),
	}
}
```

`fmt` is already imported in `kube_tool.go`.

**Verify 4c green** after 4a+4b, then `go test ./internal/evidence ./internal/subjects -race`.

## 4d — `internal/rca/rca.go` (closes #189)

Two independent changes: the flag default, and where the kube fixture lives.

**Flag** — drop the `thump-test` default, validate like `harvest.go` does for
its own required flags (`internal/harvest/harvest.go:202-205`):

```go
rig := fs.String("rig", "", "config/<rig>/whir profile to grade evidence queries against (required)")
if err := fs.Parse(args); err != nil {
	return 2
}
if *rig == "" {
	_, _ = fmt.Fprintln(stderr, "usage: rca -rig <name> [-json] [-transcripts dir] [-row substring]")
	return 2
}
```

**Fixture** — delete the hand-built `corev1.Pod` (current lines ~74-88) and
the now-unused `corev1`/`metav1`/`runtime` imports from `rca.go`; replace
with a load keyed on the rig:

```go
kubeObjects, err := loadKubeObjects(configPath(*rig, "rca", "kube-objects.yaml"))
if err != nil {
	_, _ = fmt.Fprintln(stderr, "rca:", err)
	return 1
}
```

New `internal/rca/kubeobjects.go`, following the same read-file /
`sigs.k8s.io/yaml`-Unmarshal / wrap-error shape as
`evidence.LoadEvidenceConfig`:

```go
package rca

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// kubeObject is one pod config/<rig>/rca/kube-objects.yaml declares to seed
// the graded suite's kube fake — the topology objects a rig's
// evidence-queries.yaml selectors need present to resolve, authored per rig
// because which pods exist on it is a property of its deployment, never of
// this package.
type kubeObject struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
	Phase     string            `json:"phase"`
}

// loadKubeObjects parses config/<rig>/rca/kube-objects.yaml. A rig that
// names no such file grades with an empty kube fake, never an error — not
// every rig's evidence-queries.yaml selectors need one.
func loadKubeObjects(path string) ([]runtime.Object, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied rig config path, not user input
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read kube objects %s: %w", path, err)
	}

	var file struct {
		Pods []kubeObject `json:"pods"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse kube objects %s: %w", path, err)
	}

	objs := make([]runtime.Object, len(file.Pods))
	for i, p := range file.Pods {
		objs[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace, Labels: p.Labels},
			Status:     corev1.PodStatus{Phase: corev1.PodPhase(p.Phase)},
		}
	}
	return objs, nil
}
```

New `config/thump-test/rca/kube-objects.yaml` — the only rig `task rca`
currently grades against, so the only one that needs the file yet:

```yaml
# kube-objects.yaml seeds the graded RCA suite's kube fake — the pods this
# rig's whir/evidence-queries.yaml subject rules need present to resolve a
# selector, never a claim about what's actually running on the live cluster.
pods:
  - name: rook-ceph-mon-a
    namespace: rook-ceph
    labels:
      app: rook-ceph-mon
    phase: Running
```

**Wiring fallout** — `Taskfile.yaml`'s `rca` task calls `go run ./cmd/calipers
rca` with no flag today, relying on the default. That now fails usage
validation, so it needs the flag added explicitly:

```yaml
  rca:
    desc: Run the graded RCA suite — did the reasoner cite the right evidence, not just reach the right verdict? (key-gated, not part of ci)
    cmds:
      - go run ./cmd/calipers rca -rig thump-test
```

`newHarness`/`RunCase` (`internal/rca/harness.go`) already take `profile` and
`kubeObjects ...runtime.Object` as caller-supplied parameters — their doc
comment already says this package holds no rig- or Ceph-specific knowledge
itself. No change needed there; 4d is only about who calls them and with
what.

## Verification

- `go test ./internal/evidence -run TestToolSpecs_NeverShipARigsVocabularyToADomainThatNeverAuthoredIt -v`
  red before 4a/4b, green after — paste both outputs into the vault notes.
- `go test ./internal/evidence ./internal/subjects ./internal/rca -race`.
- `task ci > /tmp/ci.log 2>&1; echo $?` then `grep -c FAIL /tmp/ci.log` — never `| tail`.
- Manual: `go run ./cmd/calipers rca` (no `-rig`) exits 2 with the usage
  line; `go run ./cmd/calipers rca -rig thump-test` runs as before.
- `rg 'ceph_daemon\|"app": "cart"' internal/evidence` should show nothing
  outside test fixtures/testdata.
