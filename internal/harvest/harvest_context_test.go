package harvest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ianeff/thump/internal/harvest"
)

func TestVerifyKubeContext_ComparesTheActiveContextAgainstTheWantedOne(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		active   string
		checkErr error
		want     string
		wantErr  bool
	}{
		"AllowsFiringWhenTheActiveContextMatchesWhatWasAsked": {
			active: "k3d-thump-dev", want: "k3d-thump-dev", wantErr: false,
		},
		"RefusesToFireWhenTheActiveContextDiffersFromWhatWasAsked": {
			active: "k3d-thump-dev", want: "gke_some-other-project_cluster", wantErr: true,
		},
		"RefusesToFireWhenTheContextCannotBeDetermined": {
			checkErr: errors.New("kubectl: command not found"), want: "k3d-thump-dev", wantErr: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			check := func(context.Context) (string, error) { return tc.active, tc.checkErr }
			err := harvest.VerifyKubeContextForTest(context.Background(), tc.want, check)
			if tc.wantErr && err == nil {
				t.Fatal("want an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatal("want no error, got", err)
			}
		})
	}
}
