package harvest_test

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

// rbacRole is the slice of a rendered Role this package's tests assert
// against.
type rbacRole struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []struct {
		APIGroups     []string `yaml:"apiGroups"`
		Resources     []string `yaml:"resources"`
		ResourceNames []string `yaml:"resourceNames"`
		Verbs         []string `yaml:"verbs"`
	} `yaml:"rules"`
}

// renderHarvestRoles runs the real chart through `helm template` (already a
// task ci dependency via chart-lint) with harvest and argocd both turned on
// — the templates render to nothing under the chart's own defaults, so a
// test against the default render would validate none of them.
func renderHarvestRoles(t *testing.T) []rbacRole {
	t.Helper()

	out, err := exec.Command("helm", "template", "../../deploy/chart/thump",
		"--set", "harvest.enabled=true",
		"--set", "harvest.operators[0].kind=User",
		"--set", "harvest.operators[0].name=someone@example.com",
		"--set", "harvest.operators[0].apiGroup=rbac.authorization.k8s.io",
		"--set", "argocd.enabled=true",
		"--show-only", "templates/rbac-harvest-chaosmesh.yaml",
		"--show-only", "templates/rbac-harvest-oteldemo.yaml",
		"--show-only", "templates/rbac-harvest-argocd.yaml").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	var roles []rbacRole
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var r rbacRole
		if err := dec.Decode(&r); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered harvest RBAC: %v", err)
		}
		if r.Kind == "Role" {
			roles = append(roles, r)
		}
	}
	return roles
}

func roleNamed(t *testing.T, roles []rbacRole, namespace, name string) rbacRole {
	t.Helper()
	for _, r := range roles {
		if r.Metadata.Namespace == namespace && r.Metadata.Name == name {
			return r
		}
	}
	t.Fatalf("no rendered Role %s/%s", namespace, name)
	return rbacRole{}
}

func TestHarvestRBAC_GrantsExactlyWhatEachScenarioRowNeeds(t *testing.T) {
	t.Parallel()
	// One row per chaos/scenarios.yaml action this chart grants for today:
	// osd-down-accelerate's fault (PodChaos, chaos-mesh) and its
	// argocd-selfheal-off precondition (Application, argocd), plus
	// cart-failure's fault (ConfigMap, otel-demo). A verb or resource this
	// test doesn't list but the Role grants is exactly as wrong as one it
	// lists but the Role is missing — both mean the grant has drifted from
	// what CommandRunner actually shells out to.
	roles := renderHarvestRoles(t)

	cases := map[string]struct {
		namespace     string
		name          string
		apiGroup      string
		resource      string
		resourceNames []string
		verbs         []string
	}{
		"chaos-mesh PodChaos (osd-down-accelerate fault)": {
			namespace: "chaos-mesh", name: "thump-harvest-chaosmesh",
			apiGroup: "chaos-mesh.org", resource: "podchaos",
			verbs: []string{"get", "create", "delete"},
		},
		"otel-demo flagd-config ConfigMap (cart-failure fault)": {
			namespace: "otel-demo", name: "thump-harvest-oteldemo",
			apiGroup: "", resource: "configmaps", resourceNames: []string{"flagd-config"},
			verbs: []string{"get", "patch"},
		},
		"argocd rook-ceph-operator Application (osd-down-accelerate precondition)": {
			namespace: "argocd", name: "thump-harvest-argocd",
			apiGroup: "argoproj.io", resource: "applications", resourceNames: []string{"rook-ceph-operator"},
			verbs: []string{"get", "patch"},
		},
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			role := roleNamed(t, roles, want.namespace, want.name)
			if len(role.Rules) != 1 {
				t.Fatalf("Role %s/%s has %d rules, want 1", want.namespace, want.name, len(role.Rules))
			}
			rule := role.Rules[0]
			if diff := cmp.Diff([]string{want.apiGroup}, rule.APIGroups); diff != "" {
				t.Errorf("apiGroups (-want +got)\n%s", diff)
			}
			if diff := cmp.Diff([]string{want.resource}, rule.Resources); diff != "" {
				t.Errorf("resources (-want +got)\n%s", diff)
			}
			if diff := cmp.Diff(want.resourceNames, rule.ResourceNames); diff != "" {
				t.Errorf("resourceNames (-want +got)\n%s", diff)
			}
			if diff := cmp.Diff(want.verbs, rule.Verbs); diff != "" {
				t.Errorf("verbs (-want +got)\n%s", diff)
			}
		})
	}
}
