package actuate_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/contract"
)

// shippedCatalog loads the catalog production runs on, so an actuator test
// asserting an exact mutation is asserting what the deployed ConfigMap says,
// not what a fixture wishes it said.
func shippedCatalog(t *testing.T) *contract.StaticCatalog {
	t.Helper()
	cat, err := contract.LoadCatalogFile(filepath.Join("..", "..", "config", "actions", "catalog.yaml"), contract.Preconditions)
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	return cat
}

func TestRunner_ExecutesAnActionBoundOnlyInConfig(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		reverse      bool
		wantReplicas float64
	}{
		"Run scales the authored deployment down when it dispatches the forward step": {
			reverse: false, wantReplicas: 2,
		},
		"Run scales the authored deployment back up when it dispatches the reverse step": {
			reverse: true, wantReplicas: 10,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cat, err := contract.LoadCatalogFile(filepath.Join("testdata", "acme-catalog.yaml"), contract.Preconditions)
			if err != nil {
				t.Fatalf("load acme catalog: %v", err)
			}
			k := &recordKube{}
			r, err := actuate.NewWith(k, cat)
			if err != nil {
				t.Fatalf("build runner from acme catalog: %v", err)
			}

			if err := r.Run(context.Background(), "acme-shed-load", tc.reverse, nil); err != nil {
				t.Fatalf("Run(acme-shed-load, reverse=%v) returned error: %v", tc.reverse, err)
			}

			wantGVR := [3]string{"apps", "v1", "deployments"}
			if k.patchGVR != wantGVR || k.patchNS != "acme" || k.patchName != "acme-api" {
				t.Errorf("patched %v %s/%s, want %v acme/acme-api", k.patchGVR, k.patchNS, k.patchName, wantGVR)
			}

			var patch struct {
				Spec struct {
					Replicas float64 `json:"replicas"`
				} `json:"spec"`
			}
			if err := json.Unmarshal([]byte(k.patchBody), &patch); err != nil {
				t.Fatalf("patch body isn't valid JSON: %v\nbody: %s", err, k.patchBody)
			}
			if diff := cmp.Diff(tc.wantReplicas, patch.Spec.Replicas); diff != "" {
				t.Error("an action authored only in YAML dispatched the wrong replica count (-want +got):", diff)
			}
		})
	}
}

func TestBindings_RejectAContractThatNamesNoExecutableMechanism(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"BoundRefs rejects a contract whose execution names a verb with no mechanism": `
- name: acme-teleport
  execution:
    forward: [{verb: teleport, namespace: acme}]`,
		"BoundRefs rejects a scale step that names no deployment": `
- name: acme-shed-load
  execution:
    forward: [{verb: scale, namespace: acme, replicas: 2}]`,
		"BoundRefs rejects a scale step that omits a replica count": `
- name: acme-shed-load
  execution:
    forward: [{verb: scale, namespace: acme, deployment: acme-api}]`,
		"BoundRefs rejects a contract that names a forward mutation but no undo": `
- name: acme-shed-load
  execution:
    forward: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 2}]`,
		"BoundRefs rejects a contract that declares no execution block at all": `
- name: acme-inert`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cat, err := contract.Load([]byte(doc), contract.Preconditions)
			if err != nil {
				t.Fatalf("load fixture catalog: %v", err)
			}
			if _, err := actuate.BoundRefs(cat); !errors.Is(err, actuate.ErrUnbindable) {
				t.Errorf("an authored action with no reachable mechanism must fail at load, got %v, want ErrUnbindable", err)
			}
		})
	}
}
