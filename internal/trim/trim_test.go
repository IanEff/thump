package trim_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/trim"
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
