package thump_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/configtest"
	"github.com/ianeff/thump/internal/forge"
	"github.com/ianeff/thump/internal/thump"
)

// TestBuildExecutor_BindsEveryContractTheShippedCatalogAuthors pins the
// production wiring path rather than the test one. Every Runner in the
// actuate suite is built with a forge already injected, so a catalog naming
// a mechanism the composition root never constructs binds fine under test
// and refuses at startup in the cluster — which is what shipping the release
// contract did. actuate.New needs a real in-cluster config before it ever
// reaches bind, so both rows go through actuate.NewWithKube over a stub
// Kube instead — the same bind logic production's buildExecutor calls, just
// reachable off-cluster.
func TestBuildExecutor_BindsEveryContractTheShippedCatalogAuthors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		forge   thump.Forge
		wantErr error
	}{
		"buildExecutor builds a live executor when a forge is configured": {
			forge: stubForge{}, wantErr: nil,
		},
		"buildExecutor refuses a live executor when the catalog names a release and no forge is configured": {
			forge: nil, wantErr: actuate.ErrUnbindable,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Thump{Executor: "live", KillSwitchPath: filepath.Join(t.TempDir(), "switch")}
			_, _, err := thump.BuildExecutorForTestWithKube(cfg, configtest.ShippedCatalog(t), tc.forge, stubKube{})

			if tc.wantErr == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("want %v building a live executor with no forge, got %v", tc.wantErr, err)
			}
		})
	}
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
