//go:build eval

package clank

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/signal"
	"sigs.k8s.io/yaml"
)

// evalCase is one row of the eval table: a committed fixture and the
// disposition a healthy reasoner should reach against the PRODUCTION
// catalog (defaultCatalog(), the same one Main wires). Unlike the
// golden-path suite (Stage 4, a scripted model), this drives the REAL
// Model — it's a score, not a proof, and it never runs in `make ci`.
type evalCase struct {
	fixture         string // file under testdata/detections/
	wantDisposition string // "propose" | "insufficient"
	wantContractRef string // checked only when wantDisposition == "propose"
	reasonContains  string // checked only when wantDisposition == "insufficient"; "" = any non-empty reason
}

func evalTable() []evalCase {
	return []evalCase{
		{
			fixture:         "node-death.yaml",
			wantDisposition: "propose",
			wantContractRef: "hold-rebalance",
		},
		{
			fixture:         "argocd-sync-burn.yaml",
			wantDisposition: "insufficient",
		},
	}
}

// TestEval_ReasonerAgainstProductionCatalog scores the table above against a
// real Anthropic model. It is gated on ANTHROPIC_API_KEY — no key, no
// asserts, just a skip — so an accidental `go test ./...` (without -tags
// eval this file isn't even compiled in, but belt and suspenders) never
// spends a token or fails a build that can't reach the network.
func TestEval_ReasonerAgainstProductionCatalog(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY unset — the eval harness needs a real model; see `make eval`")
	}

	transcripts := os.Getenv("CLANK_EVAL_TRANSCRIPTS")
	if transcripts == "" {
		transcripts = filepath.Join(os.TempDir(), "clank-eval-transcripts")
	}
	if err := os.MkdirAll(transcripts, 0o750); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	t.Logf("transcripts (read these when a row fails): %s", transcripts)

	for _, tc := range evalTable() {
		t.Run(tc.fixture, func(t *testing.T) {
			det := loadDetectionFixture(t, tc.fixture)

			l := newLoop("", t.TempDir(), t.TempDir(),
				NewAnthropicModel(apiKey), nil,
				NewIntake(noopTopology{}, noopChange{}),
				defaultCatalog(),
				NewDirStore(transcripts))

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			set, err := l.Engine.Propose(ctx, det)
			if err != nil {
				t.Fatalf("Propose: %v (see %s/%s.jsonl)", err, transcripts, det.Fingerprint)
			}

			switch tc.wantDisposition {
			case "propose":
				if len(set.Proposals) == 0 {
					t.Fatalf("want a proposal, got none — status: %+v (see %s/%s.jsonl)",
						set.Status, transcripts, det.Fingerprint)
				}
				if got := set.Proposals[0].ContractRef; got != tc.wantContractRef {
					t.Errorf("ContractRef = %q, want %q (see %s/%s.jsonl)",
						got, tc.wantContractRef, transcripts, det.Fingerprint)
				}
			case "insufficient":
				if len(set.Proposals) != 0 {
					t.Fatalf("want insufficient, got %d proposal(s) (see %s/%s.jsonl)",
						len(set.Proposals), transcripts, det.Fingerprint)
				}
				if set.Status == nil || set.Status.Reason == "" {
					t.Errorf("decline has no reason — Stage 1's payoff regressed")
				}
				if tc.reasonContains != "" && !strings.Contains(set.Status.Reason, tc.reasonContains) {
					t.Errorf("reason %q does not contain %q", set.Status.Reason, tc.reasonContains)
				}
			}
		})
	}
}

func loadDetectionFixture(t *testing.T, name string) signal.Detection {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "detections", name)) //nolint:gosec
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var det signal.Detection
	if err := yaml.Unmarshal(raw, &det); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return det
}
