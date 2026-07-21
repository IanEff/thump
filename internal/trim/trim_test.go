package trim_test

import (
	"bytes"
	"context"
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
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/trim"
	"github.com/ianeff/thump/internal/wire"
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

// TestMain_ApproveReportsPublishedNotGranted pins that approve's success line
// says the approval was *published* (async) and never claims a grant — hiss,
// not trim, decides whether an unheld or stale fingerprint is actually
// approved, so a success line implying otherwise misleads the operator.
func TestMain_ApproveReportsPublishedNotGranted(t *testing.T) {
	t.Parallel()
	outbox := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := trim.Main([]string{"approve", "fp-1", "--approver", "alice", "--outbox", outbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "published") {
		t.Errorf("approve should report the approval as published, got %q", out)
	}
	if strings.Contains(out, "approved fp-1") {
		t.Errorf("approve must not claim a grant occurred — hiss decides that, got %q", out)
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

// TestMain_ForceFailsWhenTheIncidentExistsButIsNotHeld pins that force on
// an approved (not held) incident errors cleanly, not crashes — the !ok
// and Held==nil branches are distinct failures with the same message.
func TestMain_ForceFailsWhenTheIncidentExistsButIsNotHeld(t *testing.T) {
	t.Parallel()
	inbox, outbox := t.TempDir(), t.TempDir()
	// Write a decision with VerdictApproved (not VerdictHold) — the incident
	// exists in the projection but is not held.
	approved := decision.Governed{
		Decision: decision.Decision{
			ID: "dec-1", SignalRef: "fp-1", Verdict: decision.VerdictApproved,
			RequestedBand: decision.BandActReversible, // ...
		},
	}
	writeYAML(t, filepath.Join(inbox, "decisions"), "dec-1.yaml", approved)

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"force", "fp-1", "--inbox", inbox, "--outbox", outbox}, &stdout, &stderr)

	if code == 0 {
		t.Error("want nonzero exit code for force on a non-held incident")
	}
	if !strings.Contains(stderr.String(), "not currently held") {
		t.Errorf("want 'not currently held' in stderr, got %q", stderr.String())
	}
}

// TestMain_ApproveDefaultsApproverToUSEREnvVar pins that omitting
// --approver uses $USER, not "" — the Auditable() check would catch
// an empty string, but the test pins the default explicitly.
func TestMain_ApproveDefaultsApproverToUSEREnvVar(t *testing.T) {
	// No t.Parallel(): t.Setenv forbids it outright (mutates process-global
	// state another parallel test could read mid-run).
	outbox := t.TempDir()
	t.Setenv("USER", "testuser")

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"approve", "fp-1", "--outbox", outbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	// Read back the written Approval and confirm Approver == "testuser"
	matches, err := filepath.Glob(filepath.Join(outbox, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly one approval file in outbox, got %d: %v", len(matches), matches)
	}
	raw, err := os.ReadFile(matches[0]) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var got approval.Approval
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("testuser", got.Approver); diff != "" {
		t.Error("wrong default approver (-want +got)", diff)
	}
}

// TestTick_TwoSequentialTicksFoldCumulativelyIntoTheProjection pins that
// a second Tick incorporates new boundary objects alongside the first's
// — the Projection accumulates, it doesn't reset.
func TestTick_TwoSequentialTicksFoldCumulativelyIntoTheProjection(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	// Tick 1: one detection
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", DetectedAt: time.Now()})
	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Tick 2: a second detection arrives
	writeYAML(t, filepath.Join(inbox, "detections"), "det-2.yaml",
		signal.Detection{Fingerprint: "fp-2", DetectedAt: time.Now()})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Assert: projection contains both
	incidents := tr.Proj.Snapshot()
	if len(incidents) != 2 {
		t.Errorf("want 2 incidents after two ticks, got %d", len(incidents))
	}
}

// TestMain_ApprovePublishesToNATSWhenNATSURLIsSet pins the write-path mirror
// of runSync: given --nats-url, approve must publish straight to
// thump.approvals via a live JetPublisher, not write --outbox at all.
func TestMain_ApprovePublishesToNATSWhenNATSURLIsSet(t *testing.T) {
	t.Parallel()
	url := natstest.URL(t)
	outbox := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := trim.Main([]string{"approve", "fp-1", "--approver", "alice", "--nats-url", url, "--outbox", outbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	matches, err := filepath.Glob(filepath.Join(outbox, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("--nats-url must bypass --outbox entirely, got %d file(s) written there", len(matches))
	}

	js, closeNC, err := broker.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNC()
	stream, err := js.Stream(context.Background(), broker.StreamName)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := stream.GetLastMsgForSubject(context.Background(), "thump.approvals")
	if err != nil {
		t.Fatal("want an approval on thump.approvals:", err)
	}
	var got approval.Approval
	if err := wire.Unmarshal(raw.Data, &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("fp-1", got.SignalRef); diff != "" {
		t.Error("wrong fingerprint published (-want +got)", diff)
	}
	if diff := cmp.Diff("alice", got.Approver); diff != "" {
		t.Error("wrong approver published (-want +got)", diff)
	}
}

// TestMain_ForcePublishesToNATSWhenNATSURLIsSet pins the same mirror for
// force: the read side (is fp-1 held?) still comes from --inbox — an
// operator runs `trim sync` first — but the forced Governed goes straight
// to thump.decisions, not --outbox.
func TestMain_ForcePublishesToNATSWhenNATSURLIsSet(t *testing.T) {
	t.Parallel()
	url := natstest.URL(t)
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
	code := trim.Main([]string{"force", "fp-1", "--operator", "alice", "--inbox", inbox, "--nats-url", url, "--outbox", outbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	matches, err := filepath.Glob(filepath.Join(outbox, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("--nats-url must bypass --outbox entirely, got %d file(s) written there", len(matches))
	}

	js, closeNC, err := broker.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNC()
	stream, err := js.Stream(context.Background(), broker.StreamName)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := stream.GetLastMsgForSubject(context.Background(), "thump.decisions")
	if err != nil {
		t.Fatal("want a forced decision on thump.decisions:", err)
	}
	var got decision.Governed
	if err := wire.Unmarshal(raw.Data, &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(decision.VerdictApproved, got.Decision.Verdict); diff != "" {
		t.Error("wrong verdict published (-want +got)", diff)
	}
	if !got.Decision.Forced {
		t.Error("want Forced=true on the published Governed")
	}
}

// TestMain_SyncThenIncidentsRoundTripsALiveNATSDetectionThroughTheCLI
// exercises the actual two-command operator workflow (Run 4): sync a real
// NATS stream into an inbox, then read incidents back out of it.
func TestMain_SyncThenIncidentsRoundTripsALiveNATSDetectionThroughTheCLI(t *testing.T) {
	t.Parallel()
	url := natstest.URL(t)
	js, closeNC, err := broker.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNC()
	publishTo(t, js, "thump.detections", signal.Detection{
		Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now(),
	})

	inbox := t.TempDir()
	var syncOut, syncErr bytes.Buffer
	code := trim.Main([]string{"sync", "--nats-url", url, "--inbox", inbox}, &syncOut, &syncErr)
	if code != 0 {
		t.Fatalf("want exit code 0 from sync, got %d (stderr: %s)", code, syncErr.String())
	}
	if !strings.Contains(syncOut.String(), "synced 1 object") {
		t.Errorf("want a summary line mentioning 1 synced object, got %q", syncOut.String())
	}

	var stdout, stderr bytes.Buffer
	code = trim.Main([]string{"incidents", "--json", "--inbox", inbox}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit code 0 from incidents, got %d (stderr: %s)", code, stderr.String())
	}
	var got []trim.Incident
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout was not valid JSON: %v\noutput: %s", err, stdout.String())
	}
	if len(got) != 1 || got[0].Fingerprint != "fp-1" {
		t.Errorf("want one incident for fp-1 synced from NATS, got %+v", got)
	}
}

// TestMain_SyncFailsCleanlyWithNoNATSURLConfigured pins that a missing
// --nats-url (and no $NATS_URL) is a usage error, not a panic or an opaque
// connection-refused error from a nil client.
func TestMain_SyncFailsCleanlyWithNoNATSURLConfigured(t *testing.T) {
	t.Setenv("NATS_URL", "") // no t.Parallel(): t.Setenv forbids it
	var stdout, stderr bytes.Buffer

	code := trim.Main([]string{"sync", "--inbox", t.TempDir()}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("want exit code 2 (usage error), got %d", code)
	}
	if stderr.String() == "" {
		t.Error("want a usage message on stderr, got none")
	}
}
