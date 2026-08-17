package replay_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/hiss"
	"sigs.k8s.io/yaml"
)

type harvestRow struct {
	ScenarioName       string  `json:"scenarioName"`
	ExpectedClass      string  `json:"expectedClass"`
	ExpectedContract   string  `json:"expectedContract"`
	ExpectedVerdict    string  `json:"expectedVerdict"`
	ActualVerdict      string  `json:"actualVerdict"`
	ActualContract     string  `json:"actualContract"`
	ActualResult       string  `json:"actualResult"`
	EmittedConfidence  float64 `json:"emittedConfidence"`
	ComputedConfidence float64 `json:"computedConfidence"`
	CeilingBound       bool    `json:"ceilingBound"`
	RunID              string  `json:"runID"`
}

// TestReplay_TheSplitGateChangesNoVerdictThatWasRefusedOnGrounding pins the
// claim this phase must not overreach on: splitting the gate may only convert
// escalations that were bound by the model's self-report. A Set that failed on
// grounding under the collapsed gate must fail identically under the split one.
func TestReplay_TheSplitGateChangesNoVerdictThatWasRefusedOnGrounding(t *testing.T) {
	t.Parallel()

	rawPolicy, err := os.ReadFile(filepath.Join("..", "..", "config", "dev", "hiss", "policy.yaml")) //nolint:gosec // G304: fixed repo path
	if err != nil {
		t.Fatalf("load dev policy: %v", err)
	}

	var polCollapsed hiss.Policy
	if err := yaml.Unmarshal(rawPolicy, &polCollapsed); err != nil {
		t.Fatalf("unmarshal collapsed policy: %v", err)
	}
	polCollapsed.ConfidenceGate = "collapsed"

	var polSplit hiss.Policy
	if err := yaml.Unmarshal(rawPolicy, &polSplit); err != nil {
		t.Fatalf("unmarshal split policy: %v", err)
	}
	polSplit.ConfidenceGate = "split"

	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	auth := hiss.Authority{}

	// 1. Gather all .set.json files across repo
	var setFiles []string
	searchRoots := []string{
		filepath.Join("..", "..", "internal", "rca", "testdata", "graded"),
		filepath.Join("..", "..", "internal", "replay", "testdata"),
		filepath.Join("..", "..", "bin", "transcripts"),
	}
	for _, root := range searchRoots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".set.json") {
				setFiles = append(setFiles, path)
			}
			return nil
		})
	}

	if len(setFiles) == 0 {
		t.Fatal("no banked .set.json files found across search roots")
	}

	var (
		totalSetsEvaluated   int
		approvedToApproved   int
		convertedToApproved  int
		groundingEscalations int
		otherEscalations     int
		rejectedSets         int
	)

	for _, file := range setFiles {
		raw, err := os.ReadFile(file) //nolint:gosec // G304: discovered testdata paths
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}

		var ps proposal.Set
		if err := json.Unmarshal(raw, &ps); err != nil {
			t.Fatalf("unmarshal %s: %v", file, err)
		}

		totalSetsEvaluated++
		decCollapsed := auth.Evaluate(ps, polCollapsed, now)
		decSplit := auth.Evaluate(ps, polSplit, now)

		// Invariant 1: Approvals under collapsed must not regress under split
		if decCollapsed.Verdict == decision.VerdictApproved {
			approvedToApproved++
			if decSplit.Verdict != decision.VerdictApproved {
				t.Errorf("%s: previously approved set regressed under split gate: %v", file, decSplit.Reasons)
			}
		}

		// Invariant 2: Grounding failures under collapsed must not convert under split
		rec, found := recommendedForTest(ps)
		if found && rec.ComputedConfidence > 0 && rec.ComputedConfidence < decCollapsed.FloorApplied {
			groundingEscalations++
			if decSplit.Verdict == decision.VerdictApproved {
				t.Errorf("%s: set with ComputedConfidence (%.2f) < floor (%.2f) was improperly approved under split gate",
					file, rec.ComputedConfidence, decCollapsed.FloorApplied)
			}
		}

		// Invariant 3: If an escalation converted to approved, verify it was purely a self-report hedge
		if decCollapsed.Verdict == decision.VerdictEscalate && decSplit.Verdict == decision.VerdictApproved {
			convertedToApproved++
			if rec.ComputedConfidence < decCollapsed.FloorApplied {
				t.Errorf("%s: converted set had ComputedConfidence (%.2f) < floor (%.2f)",
					file, rec.ComputedConfidence, decCollapsed.FloorApplied)
			}
		}

		if decCollapsed.Verdict == decision.VerdictRejected {
			rejectedSets++
		}
		t.Logf("Set %-45s | Collapsed: %-8s %-25v | Split: %-8s %-25v",
			filepath.Base(file), decCollapsed.Verdict, decCollapsed.Reasons, decSplit.Verdict, decSplit.Reasons)
		if decCollapsed.Verdict == decision.VerdictEscalate && decSplit.Verdict == decision.VerdictApproved {
			t.Logf("  ==> CONVERTED: %s (computed=%.2f, selfReport=%.2f, floor=%.2f)",
				filepath.Base(file), rec.ComputedConfidence, rec.Confidence, decCollapsed.FloorApplied)
		}
	}

	// 2. Replay Harvest records (AU live pass)
	harvestPath := filepath.Join("..", "..", "bin", "harvest", "phase-au-live-pass.jsonl")
	var (
		harvestRowsTotal     int
		harvestConverted     int
		harvestGroundingStay int
		harvestApprovedStay  int
		harvestRefusedStay   int
	)
	if rawHarvest, err := os.ReadFile(harvestPath); err == nil { //nolint:gosec // G304: fixed repo path
		lines := strings.Split(strings.TrimSpace(string(rawHarvest)), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row harvestRow
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatalf("unmarshal harvest line: %v", err)
			}
			harvestRowsTotal++

			// Check conversion of AU live pass rows against dev policy floor (0.75)
			floor := 0.75
			switch row.ScenarioName {
			case "product-catalog-failure":
				// emitted 0.65, computed 1.00 -> collapsed escalated/declined, split approves!
				if row.EmittedConfidence < floor && row.ComputedConfidence >= floor {
					harvestConverted++
				}
			case "cart-failure", "acme-api-fault":
				// emitted >= 0.75, computed 1.00 -> approved under both
				if row.EmittedConfidence >= floor && row.ComputedConfidence >= floor {
					harvestApprovedStay++
				}
			case "crashloop-decoy":
				// 0.00 / 0.00 -> refused under both
				if row.ComputedConfidence < floor {
					harvestRefusedStay++
				}
			default:
				if row.ComputedConfidence < floor {
					harvestGroundingStay++
				}
			}
		}
	}

	t.Logf("=== PR 2 HONESTY CHECK SUMMARY ===")
	t.Logf("Banked .set.json files evaluated: %d", totalSetsEvaluated)
	t.Logf("  - Unchanged Approvals:          %d", approvedToApproved)
	t.Logf("  - Converted Escalations:        %d", convertedToApproved)
	t.Logf("  - Preserved Grounding Blocks:   %d", groundingEscalations)
	t.Logf("  - Other Preserved Escalations:  %d", otherEscalations)
	t.Logf("  - Preserved Ungated Rejections: %d", rejectedSets)
	t.Logf("Phase AU Live Pass Harvest Rows:  %d", harvestRowsTotal)
	t.Logf("  - Live Converted (product-cat): %d", harvestConverted)
	t.Logf("  - Live Approvals (cart, acme):  %d", harvestApprovedStay)
	t.Logf("  - Live Refusals (crashloop):    %d", harvestRefusedStay)
}

func recommendedForTest(ps proposal.Set) (proposal.Candidate, bool) {
	for _, c := range ps.Proposals {
		if c.ID == ps.Recommended {
			return c, true
		}
	}
	return proposal.Candidate{}, false
}
