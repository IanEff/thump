package hiss_test

import (
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// admissionPolicy is the slice of a ValidatingAdmissionPolicy or
// MutatingAdmissionPolicy this package's tests assert against.
type admissionPolicy struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		FailurePolicy    string `yaml:"failurePolicy"`
		MatchConstraints struct {
			ResourceRules []struct {
				Operations []string `yaml:"operations"`
				Resources  []string `yaml:"resources"`
			} `yaml:"resourceRules"`
		} `yaml:"matchConstraints"`
		Validations []struct {
			Expression string `yaml:"expression"`
		} `yaml:"validations"`
	} `yaml:"spec"`
}

// renderAdmissionPolicies runs the real chart through `helm template`
// (already a task ci dependency via chart-lint) so these tests read the
// bytes the API server is handed, not a Go copy that could drift from them.
func renderAdmissionPolicies(t *testing.T) []admissionPolicy {
	t.Helper()

	out, err := exec.Command("helm", "template", "../../deploy/chart/thump",
		"--set", "approvalRequests.enabled=true",
		"--show-only", "templates/admissionpolicy-approvalrequest.yaml").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	var policies []admissionPolicy
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var p admissionPolicy
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered admission policies: %v", err)
		}
		if p.Kind != "" {
			policies = append(policies, p)
		}
	}
	return policies
}

func policyNamed(t *testing.T, policies []admissionPolicy, kind, name string) admissionPolicy {
	t.Helper()
	for _, p := range policies {
		if p.Kind == kind && p.Metadata.Name == name {
			return p
		}
	}
	t.Fatalf("no %s named %q among the rendered policies", kind, name)
	return admissionPolicy{}
}

// TestAdmissionPolicies_ApprovalRequestIsBornUndecided guards the property the
// controller's fail-closed check cannot establish on its own: the approver
// stamp only fires on UPDATE, so a resource that could be created already
// carrying spec.decision would be a decision with no authenticated subject
// behind it — approval by whoever holds create rights, which is not the
// authority model this resource claims.
func TestAdmissionPolicies_ApprovalRequestIsBornUndecided(t *testing.T) {
	t.Parallel()

	got := policyNamed(t, renderAdmissionPolicies(t), "ValidatingAdmissionPolicy", "approvalrequest-born-undecided")

	if got.Spec.FailurePolicy != "Fail" {
		t.Errorf("failurePolicy is %q — an admission guard that fails open is not a guard", got.Spec.FailurePolicy)
	}

	covered := false
	for _, rule := range got.Spec.MatchConstraints.ResourceRules {
		if slices.Contains(rule.Operations, "CREATE") && slices.Contains(rule.Resources, "approvalrequests") {
			covered = true
		}
	}
	if !covered {
		t.Error("the policy must match CREATE on approvalrequests — UPDATE-only coverage is the hole it exists to close")
	}

	if len(got.Spec.Validations) == 0 {
		t.Fatal("the policy must carry a validation rejecting a decision set at creation")
	}
}

// TestAdmissionPolicies_ForceIsNotAValueTheResourceAccepts pins the authority
// split at the API server, not just in the controller. Bypassing hiss's risk
// gate is break-glass and lives in trim; a value on this resource would put it
// behind the same RBAC verb as an ordinary approval, one word apart.
func TestAdmissionPolicies_ForceIsNotAValueTheResourceAccepts(t *testing.T) {
	t.Parallel()

	got := policyNamed(t, renderAdmissionPolicies(t), "ValidatingAdmissionPolicy", "approvalrequest-immutable-fields")

	for _, v := range got.Spec.Validations {
		if strings.Contains(v.Expression, "spec.decision") && strings.Contains(v.Expression, "force") {
			t.Errorf("the decision enum still admits force: %s", v.Expression)
		}
	}
}
