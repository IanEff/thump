package scorecard_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ianeff/thump/internal/scorecard"
)

// TestMain_PrintsTheHumanReportAgainstAResultsFileByDefault pins the
// --results flag as the file-backed alternative to stdin — nothing else
// exercises Main reading a real path rather than a piped buffer.
func TestMain_PrintsTheHumanReportAgainstAResultsFileByDefault(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := scorecard.Main([]string{"--results", "testdata/results.jsonl"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("runs=10 hits=3")) {
		t.Errorf("want the headline rate in stdout, got %s", stdout.String())
	}
}

// TestMain_PrintsAJSONReportWhenAsked pins --json as a structurally
// different output, not just the human table re-indented — a caller
// scripting off the rate needs a real decode target.
func TestMain_PrintsAJSONReportWhenAsked(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := scorecard.Main([]string{"--results", "testdata/results.jsonl", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	var got scorecard.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output didn't decode as a Report: %v (stdout: %s)", err, stdout.String())
	}
	if got.N != 10 || got.Hits != 3 {
		t.Errorf("want N=10 Hits=3, got N=%d Hits=%d", got.N, got.Hits)
	}
}

// TestMain_FailsWhenTheResultsFileDoesNotExist pins that a bad --results
// path is Main's own exit-1 error, not a silently empty Report.
func TestMain_FailsWhenTheResultsFileDoesNotExist(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := scorecard.Main([]string{"--results", "testdata/does-not-exist.jsonl"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("want exit 1 for a missing results file, got %d", code)
	}
}
