package tune_test

import (
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/tune"
)

// TestTune_LeavesEveryConfigFileByteIdentical pins the authority boundary. The
// tuning surface reads and proposes; the authority to write the governance
// floors hiss enforces has never been granted, and a flag added quietly is how
// it would get granted without anyone deciding to.
func TestTune_LeavesEveryConfigFileByteIdentical(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ file string }{
		"tune leaves config/clank/weights.yaml untouched": {file: "../../config/clank/weights.yaml"},
		"tune leaves config/hiss/policy.yaml untouched":   {file: "../../config/hiss/policy.yaml"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			before, err := os.ReadFile(tc.file) //nolint:gosec // G304: fixed config path, not user input
			if err != nil {
				t.Fatal(err)
			}
			if code := tune.Main([]string{"--json"}, io.Discard, io.Discard); code > 1 {
				t.Fatalf("sweep exited %d", code)
			}
			after, err := os.ReadFile(tc.file) //nolint:gosec // G304: fixed config path, not user input
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(string(before), string(after)); diff != "" {
				t.Error("tune mutated a config file it must not touch", diff)
			}
		})
	}
}

// TestMain_RejectsAnApplyFlagRatherThanIgnoringIt keeps the refusal loud. An
// unknown flag that exits 2 is a refusal a human reads; a flag silently ignored
// is one somebody assumes worked.
func TestMain_RejectsAnApplyFlagRatherThanIgnoringIt(t *testing.T) {
	t.Parallel()

	if got := tune.Main([]string{"--apply"}, io.Discard, io.Discard); got != 2 {
		t.Error("want exit 2 for --apply", got)
	}
}
