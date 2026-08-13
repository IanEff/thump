package thump_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/configtest"
	"github.com/ianeff/thump/internal/forge"
	"github.com/ianeff/thump/internal/thump"
	"gopkg.in/yaml.v3"
)

// TestBuildExecutor_BindsEveryContractTheShippedCatalogAuthors pins the
// production wiring path rather than the test one: a forge configured at
// buildExecutor's call site produces a live executor. Every Runner in the
// actuate suite is built with a forge already injected, so this is the only
// coverage of that composition-root wiring off-cluster. actuate.New needs a
// real in-cluster config before it ever reaches bind, so it goes through
// actuate.NewWithKube over a stub Kube instead — the same bind logic
// production's buildExecutor calls, just reachable off-cluster. The sibling
// refusal case — a catalog naming a mechanism no forge backs — is pinned
// twice over by TestNewWith_RefusesAReleaseContractWhenNoForgeIsWired
// against a fixture that always authors the mechanism, and by
// TestEveryProfileCatalog_BindsUnderThatProfilesOwnWiring against every
// profile's real config.
func TestBuildExecutor_BindsEveryContractTheShippedCatalogAuthors(t *testing.T) {
	t.Parallel()

	cfg := config.Thump{Executor: "live", KillSwitchPath: filepath.Join(t.TempDir(), "switch")}
	_, _, err := thump.BuildExecutorForTestWithKube(cfg, configtest.ShippedCatalog(t), stubForge{}, stubKube{})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEveryProfileCatalog_BindsUnderThatProfilesOwnWiring is the completeness
// guard shipping the release contract exposed: a profile's catalog must bind
// under the exact forge wiring that profile's own deploy/tilt-values file
// configures, not a hand-maintained claim about what that file says. Profiles
// are discovered from config/*/actions/catalog.yaml so a new profile is
// covered the day it ships a catalog, and each one's forge requirement is
// read from deploy/tilt-values-<profile>.yaml rather than hardcoded, so this
// test cannot pass while silently exercising the wrong wiring.
func TestEveryProfileCatalog_BindsUnderThatProfilesOwnWiring(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	tests := map[string]struct {
		profile string
		forge   thump.Forge // nil means the profile's values file configures no forge
	}{}
	for _, profile := range profilesWithCatalog(t, root) {
		var f thump.Forge
		if profileConfiguresForge(t, root, profile) {
			f = stubForge{}
		}
		name := "the " + profile + " profile binds under the forge its own tilt-values file configures"
		tests[name] = struct {
			profile string
			forge   thump.Forge
		}{profile: profile, forge: f}
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Thump{Executor: "live", KillSwitchPath: filepath.Join(t.TempDir(), "switch")}
			cat := configtest.CatalogForProfile(t, tc.profile)

			_, _, err := thump.BuildExecutorForTestWithKube(cfg, cat, tc.forge, stubKube{})

			if diff := cmp.Diff(error(nil), err, cmpopts.EquateErrors()); diff != "" {
				t.Error("profile catalog does not bind under its own wiring", diff)
			}
		})
	}
}

// repoRoot walks up from the working directory to the enclosing go.mod, the
// same resolution configtest's own profilePath uses, so a test nested below
// internal/<pkg> finds the same repo root regardless of how deep it sits.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s: cannot locate repo root", dir)
		}
		dir = parent
	}
}

// profilesWithCatalog lists every config/<profile> directory that authors an
// actions/catalog.yaml — the set the chart can actually deploy a catalog
// for, as opposed to every directory under config/.
func profilesWithCatalog(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "config"))
	if err != nil {
		t.Fatalf("read config/: %v", err)
	}
	var profiles []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "config", e.Name(), "actions", "catalog.yaml")); err == nil {
			profiles = append(profiles, e.Name())
		}
	}
	return profiles
}

// profileConfiguresForge reports whether a profile's deploy/tilt-values file
// sets forge.repo — the same field the chart's FORGE_REPO env guard checks.
func profileConfiguresForge(t *testing.T, root, profile string) bool {
	t.Helper()
	path := filepath.Join(root, "deploy", "tilt-values-"+profile+".yaml")
	data, err := os.ReadFile(path) //nolint:gosec // G304: profile is discovered from config/ subdirectory names, not user input
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var values struct {
		Forge struct {
			Repo string `yaml:"repo"`
		} `yaml:"forge"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return values.Forge.Repo != ""
}

// stubKube satisfies actuate.Kube without a cluster — the guard test binds
// the shipped catalog, it never dispatches through Kube, so every method
// here is unreached and only needs to compile.
type stubKube struct{}

func (stubKube) Exec(context.Context, string, string, []string) error { return nil }
func (stubKube) Patch(context.Context, string, string, string, string, string, []byte) error {
	return nil
}

func (stubKube) GetConfigMapKey(context.Context, string, string, string) (string, error) {
	return "", nil
}

// stubForge satisfies thump.Forge without a network call — the guard test
// only needs a forge configured, it never calls through it.
type stubForge struct{}

func (stubForge) Read(context.Context, string) ([]byte, error)       { return nil, nil }
func (stubForge) Cut(context.Context, forge.Release) (string, error) { return "", nil }
func (stubForge) Withdraw(context.Context, string) (bool, error)     { return false, nil }
