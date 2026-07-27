package trim_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/trim"
	"sigs.k8s.io/yaml"
)

// TestMain_IncidentsJSONPrintsCleanParseableJSON pins the W-R4 claim: piping
// `trim incidents --json` into a JSON decoder must work, with nothing else
// — no log lines, no styled text — sharing stdout with the payload.
func TestMain_IncidentsJSONPrintsCleanParseableJSON(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"incidents", "--json", "--inbox", inbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	var got []trim.Incident
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout was not valid JSON: %v\noutput: %s", err, stdout.String())
	}
	if len(got) != 1 || got[0].Fingerprint != "fp-1" {
		t.Errorf("want one incident for fp-1, got %+v", got)
	}
}

// TestMain_IncidentsPrintsHumanReadableTextByDefault pins the human path:
// without --json, the same data comes back through renderIncidents, not a
// JSON blob.
func TestMain_IncidentsPrintsHumanReadableTextByDefault(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"incidents", "--inbox", inbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fp-1") {
		t.Errorf("want stdout to mention fp-1, got %q", stdout.String())
	}
	if json.Valid(stdout.Bytes()) {
		t.Error("want plain text without --json, got valid JSON")
	}
}

// TestMain_ReturnsUsageErrorWithNoArgs pins that an empty invocation fails
// loud rather than silently doing nothing.
func TestMain_ReturnsUsageErrorWithNoArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := trim.Main(nil, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code for no arguments, got 0")
	}
	if stderr.String() == "" {
		t.Error("want a usage message on stderr, got none")
	}
}

// TestMain_ReturnsUsageErrorForAnUnknownCommand pins the same failure shape
// for a typo'd subcommand — never routed silently to a no-op.
func TestMain_ReturnsUsageErrorForAnUnknownCommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := trim.Main([]string{"bogus"}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code for an unknown command, got 0")
	}
	if stderr.String() == "" {
		t.Error("want an error message on stderr, got none")
	}
}

func TestMain_ApprovePublishesAnAuditableApproval(t *testing.T) {
	t.Parallel()
	outbox := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := trim.Main([]string{"approve", "fp-1", "--approver", "alice", "--outbox", outbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	matches, err := filepath.Glob(filepath.Join(outbox, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly one written Approval, got %d", len(matches))
	}
	raw, err := os.ReadFile(matches[0]) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var got approval.Approval
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("fp-1", got.SignalRef); diff != "" {
		t.Error("wrong fingerprint written (-want +got)", diff)
	}
	if diff := cmp.Diff("alice", got.Approver); diff != "" {
		t.Error("wrong approver written (-want +got)", diff)
	}
	if err := got.Auditable(); err != nil {
		t.Error("written Approval must be Auditable:", err)
	}
}

func TestMain_ApproveRequiresExactlyOneFingerprintArgument(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := trim.Main([]string{"approve", "--outbox", t.TempDir()}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code with no fingerprint argument, got 0")
	}
}

func TestMain_ForcePublishesAForcedGovernedStraightToDecisions(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	held := decision.Governed{
		Decision: decision.Decision{
			ID: "dec-1", SignalRef: "fp-1", Verdict: decision.VerdictHold,
			RequestedBand: decision.BandActDisruptive, RiskBand: decision.BandActDisruptive,
			PolicyVersion: "policy-v3", EvaluatedAt: time.Now(),
			Reasons: []string{decision.ReasonRiskCeiling},
		},
		Set: proposal.Set{SignalRef: "fp-1"},
	}
	writeYAML(t, filepath.Join(inbox, "decisions"), "dec-1.yaml", held)

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"force", "fp-1", "--operator", "alice", "--inbox", inbox, "--outbox", outbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	matches, err := filepath.Glob(filepath.Join(outbox, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly one forced Governed written, got %d", len(matches))
	}
	raw, err := os.ReadFile(matches[0]) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var got decision.Governed
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(decision.VerdictApproved, got.Decision.Verdict); diff != "" {
		t.Error("wrong verdict (-want +got)", diff)
	}
	if !got.Decision.Forced {
		t.Error("want Forced=true on a break-glass decision")
	}
	if diff := cmp.Diff("alice", got.Decision.Operator); diff != "" {
		t.Error("wrong operator recorded (-want +got)", diff)
	}
	if err := got.Decision.Auditable(); err != nil {
		t.Error("forced decision must be Auditable:", err)
	}
}

func TestMain_ForceFailsWhenTheFingerprintIsNotCurrentlyHeld(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	var stdout, stderr bytes.Buffer

	code := trim.Main([]string{"force", "no-such-fp", "--inbox", inbox, "--outbox", outbox}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code for a fingerprint with nothing held, got 0")
	}
	if stderr.String() == "" {
		t.Error("want an explanatory message on stderr, got none")
	}
}

// testSealKeyB64 is a 32-byte AES-256 key, base64-encoded — the same value
// internal/config's own tests use, standing in for THUMP_SEAL_KEY.
const testSealKeyB64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

func sealFile(t *testing.T, plaintext []byte) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(testSealKeyB64)
	if err != nil {
		t.Fatal(err)
	}
	var key sealbox.Key
	copy(key[:], raw)
	sealed, err := key.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "segment.sealed")
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMain_UnsealDecryptsAFileSealedWithTheConfiguredKey(t *testing.T) {
	// Not t.Parallel(): t.Setenv panics alongside t.Parallel.
	plaintext := []byte(`{"fingerprint":"fp-1"}`)
	path := sealFile(t, plaintext)
	t.Setenv("THUMP_SEAL_KEY", testSealKeyB64)

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"unseal", path}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	if diff := cmp.Diff(string(plaintext), stdout.String()); diff != "" {
		t.Error("unsealed stdout didn't match the original plaintext", diff)
	}
}

func TestMain_UnsealFailsWithoutTHUMPSealKeySet(t *testing.T) {
	path := sealFile(t, []byte("payload"))
	t.Setenv("THUMP_SEAL_KEY", "")

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"unseal", path}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code with THUMP_SEAL_KEY unset, got 0")
	}
	if !strings.Contains(stderr.String(), "THUMP_SEAL_KEY") {
		t.Errorf("want stderr to name the missing var, got %q", stderr.String())
	}
}

func TestMain_UnsealFailsOnATamperedFile(t *testing.T) {
	path := sealFile(t, []byte("payload"))
	t.Setenv("THUMP_SEAL_KEY", testSealKeyB64)

	sealed, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0xFF
	if err := os.WriteFile(path, sealed, 0o600); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"unseal", path}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code for a tampered file, got 0")
	}
}

func TestMain_UnsealRequiresExactlyOneFileArgument(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := trim.Main([]string{"unseal"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("want exit code 2 for a missing file argument, got %d", code)
	}
}
