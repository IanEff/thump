package validate_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/validate"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("cannot find go.mod above %s", wd)
		}
		dir = parent
	}
}

func TestValidateProfile_PassesOnShippedProfiles(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		relDir string
	}{
		"ValidateProfile passes on dev profile": {
			relDir: "config/dev",
		},
		"ValidateProfile passes on thump-test profile": {
			relDir: "config/thump-test",
		},
		"ValidateProfile passes on acme fixture domain": {
			relDir: "test/onboarding/testdata/acme",
		},
	}

	root := findRepoRoot(t)

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			profileDir := filepath.Join(root, tc.relDir)
			res, err := validate.Profile(profileDir)
			if err != nil {
				t.Fatalf("expected profile at %s to validate cleanly, got err: %v (errors: %v)", tc.relDir, err, res.Errors)
			}
			if len(res.Errors) > 0 {
				t.Fatalf("expected zero validation errors for %s, got: %v", tc.relDir, res.Errors)
			}
			if res.Actions == 0 {
				t.Errorf("expected > 0 actions validated for %s", tc.relDir)
			}
		})
	}
}

func TestProfile_CatchesInvalidCatalogAndMissingPolicyFloors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		setup    func(t *testing.T, dir string)
		wantSub  string
		wantRule string
	}{
		"Profile rejects catalog with unknown failure class": {
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "actions", "catalog.yaml"), `
- name: bad-action
  applicableFailureClasses: [unknown_bogus_class]
  applicableTiers: [tier-1]
  blastTier: low
  reversal:
    method: undo-bad
    fallback: page-oncall
  execution:
    forward: [{verb: restart, namespace: default, deployment: app}]
    reverse: [{verb: restart, namespace: default, deployment: app}]
  successCriteria:
    metric: app_errors
    target: "app_errors < 0.01"
    window: 300000000000
`)
				writeTestFile(t, filepath.Join(dir, "actions", "failure-classes.yaml"), `
- class: service_failure
  description: Service failure
`)
				writeTestFile(t, filepath.Join(dir, "hiss", "policy.yaml"), `
version: v1
floors:
  tier-1:
    service_failure: 0.75
maxBand:
  tier-1: act_reversible
autoBand:
  tier-1: act_reversible
requireReversal: true
`)
			},
			wantSub: "not a FailureClass const",
		},
		"Profile rejects policy missing floor for actuatable class": {
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "actions", "catalog.yaml"), `
- name: valid-action
  applicableFailureClasses: [service_failure]
  applicableTiers: [tier-1]
  blastTier: low
  reversal:
    method: undo-valid
    fallback: page-oncall
  execution:
    forward: [{verb: restart, namespace: default, deployment: app}]
    reverse: [{verb: restart, namespace: default, deployment: app}]
  successCriteria:
    metric: app_errors
    target: "app_errors < 0.01"
    window: 300000000000
`)
				writeTestFile(t, filepath.Join(dir, "actions", "failure-classes.yaml"), `
- class: service_failure
  description: Service failure
`)
				writeTestFile(t, filepath.Join(dir, "hiss", "policy.yaml"), `
version: v1
floors:
  tier-1:
    resource_exhaustion: 0.75
maxBand:
  tier-1: act_reversible
autoBand:
  tier-1: act_reversible
requireReversal: true
`)
			},
			wantSub: "missing confidence floor",
		},
		"Profile rejects action missing reverse execution": {
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "actions", "catalog.yaml"), `
- name: no-reverse
  applicableFailureClasses: [service_failure]
  applicableTiers: [tier-1]
  blastTier: low
  reversal:
    method: undo-valid
    fallback: page-oncall
  execution:
    forward: [{verb: restart, namespace: default, deployment: app}]
    reverse: []
  successCriteria:
    metric: app_errors
    target: "app_errors < 0.01"
    window: 300000000000
`)
				writeTestFile(t, filepath.Join(dir, "actions", "failure-classes.yaml"), `
- class: service_failure
  description: Service failure
`)
				writeTestFile(t, filepath.Join(dir, "hiss", "policy.yaml"), `
version: v1
floors:
  tier-1:
    service_failure: 0.75
maxBand:
  tier-1: act_reversible
autoBand:
  tier-1: act_reversible
requireReversal: true
`)
			},
			wantSub: "no reverse execution steps",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tc.setup(t, dir)

			res, err := validate.Profile(dir)
			if err == nil && len(res.Errors) == 0 {
				t.Fatalf("expected validation failure containing %q, got nil error", tc.wantSub)
			}
			found := false
			for _, e := range res.Errors {
				if errors.Is(e, validate.ErrValidationFailed) || containsSub(e.Error(), tc.wantSub) {
					found = true
					break
				}
			}
			if err != nil && containsSub(err.Error(), tc.wantSub) {
				found = true
			}
			if !found {
				t.Errorf("validation error did not contain %q; got err: %v, res.Errors: %v", tc.wantSub, err, res.Errors)
			}
		})
	}
}

func TestAll_ValidatesAllShippedProfiles(t *testing.T) {
	t.Parallel()
	root := findRepoRoot(t)

	results, err := validate.All(root)
	if err != nil {
		t.Fatalf("All returned unexpected error: %v", err)
	}

	if diff := cmp.Diff(3, len(results)); diff != "" {
		t.Errorf("All wrong profile count (-want +got):\n%s", diff)
	}
	for _, res := range results {
		if len(res.Errors) > 0 {
			t.Errorf("profile %s had unexpected validation errors: %v", res.Profile, res.Errors)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func containsSub(s, substr string) bool {
	return filepath.Clean(s) != "" && len(substr) > 0 && len(s) >= len(substr) && (s == substr || filepath.Base(s) == substr || (len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(substr) > 0 && func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	}()))
}
