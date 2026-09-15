package calipers_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/calipers"
	"github.com/ianeff/thump/internal/decisiontest"
	"github.com/ianeff/thump/internal/incident"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/wire"
	"sigs.k8s.io/yaml"
)

// TestMain_IncidentsJSONPrintsCleanParseableJSON pins the W-R4 claim: piping
// `calipers incidents --json` into a JSON decoder must work, with nothing
// else — no log lines, no styled text — sharing stdout with the payload.
func TestMain_IncidentsJSONPrintsCleanParseableJSON(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"incidents", "--json", "--inbox", inbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	var got []incident.Incident
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
	code := calipers.Main([]string{"incidents", "--inbox", inbox}, &stdout, &stderr)

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

// TestMain_IncidentsWithAFingerprintRendersTheDetailView pins that
// shiftPositional routes a bare fingerprint to the kubectl-describe-shaped
// detail view instead of the one-line list — and, since the fingerprint has
// to survive alongside --inbox, this is also the regression test for
// runIncidents once having parsed args instead of the shifted rest.
func TestMain_IncidentsWithAFingerprintRendersTheDetailView(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})
	writeYAML(t, filepath.Join(inbox, "decisions"), "dec-1.yaml", decisiontest.Held("fp-1"))

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"incidents", "fp-1", "--inbox", inbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Verdict:") {
		t.Errorf("want the detail view's Verdict line, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "fp-1") {
		t.Errorf("want stdout to mention fp-1, got %q", stdout.String())
	}
}

// TestMain_IncidentsRenderPrintsFullRecordArtifact pins that
// `calipers incidents --render <fingerprint>` renders the full Record audit
// artifact containing all sections rather than the short detail view.
func TestMain_IncidentsRenderPrintsFullRecordArtifact(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	writeYAML(t, filepath.Join(inbox, "detections"), "det-1.yaml",
		signal.Detection{Fingerprint: "fp-1", OriginService: "checkout-api", DetectedAt: time.Now()})
	writeYAML(t, filepath.Join(inbox, "decisions"), "dec-1.yaml", decisiontest.Held("fp-1"))

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"incidents", "--render", "fp-1", "--inbox", inbox}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "INCIDENT AUDIT RECORD: fp-1") {
		t.Errorf("want artifact header, got:\n%s", got)
	}
	if !strings.Contains(got, "1. WHAT FIRED") {
		t.Errorf("want section 1 'WHAT FIRED', got:\n%s", got)
	}
	if !strings.Contains(got, "4. WHAT GOVERNANCE RULED") {
		t.Errorf("want section 4 'WHAT GOVERNANCE RULED', got:\n%s", got)
	}
}

// TestMain_IncidentsWithAnUnknownFingerprintFails pins the not-found path: a
// fingerprint absent from the projection is an error, not a silent empty
// detail view.
func TestMain_IncidentsWithAnUnknownFingerprintFails(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"incidents", "fp-missing", "--inbox", inbox}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("want exit code 1, got %d (stdout: %s)", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "no incident at fingerprint") {
		t.Errorf("want stderr to explain the miss, got %q", stderr.String())
	}
}

func TestMain_ApprovePublishesAnAuditableApproval(t *testing.T) {
	t.Parallel()
	outbox := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := calipers.Main([]string{"approve", "fp-1", "--approver", "alice", "--outbox", outbox}, &stdout, &stderr)

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
	raw, err := os.ReadFile(matches[0]) //nolint:gosec // G304: matches came from filepath.Glob under t.TempDir()
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

	code := calipers.Main([]string{"approve", "--outbox", t.TempDir()}, &stdout, &stderr)

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
	code := calipers.Main([]string{"force", "fp-1", "--operator", "alice", "--inbox", inbox, "--outbox", outbox}, &stdout, &stderr)

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
	raw, err := os.ReadFile(matches[0]) //nolint:gosec // G304: matches came from filepath.Glob under t.TempDir()
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

	code := calipers.Main([]string{"force", "no-such-fp", "--inbox", inbox, "--outbox", outbox}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code for a fingerprint with nothing held, got 0")
	}
	if stderr.String() == "" {
		t.Error("want an explanatory message on stderr, got none")
	}
}

// TestMain_ForceFindsAHeldDecisionOverNATSWithNoInboxOnDisk pins the two
// halves to one transport. force published over --nats-url but read its
// held Governed from --inbox, so against a broker-mode cluster — where no
// inbox exists — break-glass reported every fingerprint as not held.
func TestMain_ForceFindsAHeldDecisionOverNATSWithNoInboxOnDisk(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	url := natstest.URL(t)
	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	if err := publish.NewJetPublisher[decision.Governed](js).
		Publish(ctx, "thump.decisions", decisiontest.Held("fp-1")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"force", "fp-1", "--operator", "alice", "--nats-url", url}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
}

// testSealKeyB64 is a 32-byte AES-256 key, base64-encoded — the same value
// internal/config's own tests use, standing in for THUMP_SEAL_KEY.
const testSealKeyB64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

// sealSegment writes plaintext through the same objectstore.EncryptingSink
// the shipping side seals with — internal/unseal.Main reads the WAL segment
// envelope, not a bare sealbox.Key.Seal blob, so a routing test has to feed
// it the real shape or it proves nothing about the wiring.
func sealSegment(t *testing.T, key sealbox.Key, plaintext []byte) string {
	t.Helper()
	inner := &captureSink{}
	sink := &objectstore.EncryptingSink{Inner: inner, Key: key}
	if err := sink.Put(context.Background(), "seg-1", bytes.NewReader(plaintext)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "segment.sealed")
	if err := os.WriteFile(path, inner.body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type captureSink struct{ body []byte }

func (c *captureSink) Put(_ context.Context, _ string, r io.Reader) error {
	b, err := io.ReadAll(r)
	c.body = b
	return err
}

// TestMain_UnsealRoutesToInternalUnsealAndPrintsTheSegmentSummary pins that
// "calipers unseal" reaches internal/unseal.Main rather than a second,
// divergent implementation — the full behavior (key validation, tamper
// detection, per-object summaries) is internal/unseal's own test coverage;
// this only has to prove the wiring.
func TestMain_UnsealRoutesToInternalUnsealAndPrintsTheSegmentSummary(t *testing.T) {
	// Not t.Parallel(): t.Setenv panics alongside t.Parallel.
	raw, err := base64.StdEncoding.DecodeString(testSealKeyB64)
	if err != nil {
		t.Fatal(err)
	}
	var key sealbox.Key
	copy(key[:], raw)

	set := proposal.Set{
		SignalRef: "fp-1", Recommended: "p1",
		Proposals: []proposal.Candidate{{ID: "p1", ContractRef: "restart-pod", Confidence: 0.5}},
	}
	line, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	path := sealSegment(t, key, append(line, '\n'))
	t.Setenv("THUMP_SEAL_KEY", testSealKeyB64)

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"unseal", path}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fp-1") {
		t.Errorf("want the unsealed ProposalSet's fingerprint in stdout, got %q", stdout.String())
	}
}

func TestMain_UnsealFailsWithoutTHUMPSealKeySet(t *testing.T) {
	// Not t.Parallel(): t.Setenv panics alongside t.Parallel.
	path := sealSegment(t, sealbox.Key{}, []byte("payload\n"))
	t.Setenv("THUMP_SEAL_KEY", "")

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"unseal", path}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code with THUMP_SEAL_KEY unset, got 0")
	}
	if !strings.Contains(stderr.String(), "THUMP_SEAL_KEY") {
		t.Errorf("want stderr to name the missing var, got %q", stderr.String())
	}
}

func TestMain_UnsealRequiresAtLeastOneFileArgument(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := calipers.Main([]string{"unseal"}, &stdout, &stderr)

	if code == 0 {
		t.Error("want a nonzero exit code for a missing file argument, got 0")
	}
}

// TestMain_ApprovePublishesToThumpApprovalsOverNATSWhenNATSURLIsSet pins the
// V2 gap: without this leg, `calipers approve` wrote a file a broker-mode
// hiss never reads, and the operator surface was unreachable in the cluster.
func TestMain_ApprovePublishesToThumpApprovalsOverNATSWhenNATSURLIsSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := natstest.URL(t)
	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"approve", "fp-1", "--approver", "alice", "--nats-url", url}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	stream, err := js.Stream(ctx, broker.StreamName)
	if err != nil {
		t.Fatal("stream:", err)
	}
	raw, err := stream.GetLastMsgForSubject(ctx, "thump.approvals")
	if err != nil {
		t.Fatal("approval never reached thump.approvals:", err)
	}
	var got approval.Approval
	if err := wire.Unmarshal(raw.Data, &got); err != nil {
		t.Fatal("wire bytes didn't decode:", err)
	}
	if diff := cmp.Diff("fp-1", got.SignalRef); diff != "" {
		t.Error("wrong fingerprint published (-want +got)", diff)
	}
}

// TestMain_ForcePublishesToThumpDecisionsOverNATSWhenNATSURLIsSet mirrors the
// approve case for force's break-glass path (D-9): a forced Governed a
// broker-mode thump can't read isn't a working override. The held decision
// it forces past is published over NATS, not written to --inbox — with one
// transport choice serving both halves (Step 3), a broker-mode force never
// looks at disk.
func TestMain_ForcePublishesToThumpDecisionsOverNATSWhenNATSURLIsSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := natstest.URL(t)
	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	if err := publish.NewJetPublisher[decision.Governed](js).
		Publish(ctx, "thump.decisions", decisiontest.Held("fp-1")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := calipers.Main([]string{"force", "fp-1", "--operator", "alice", "--nats-url", url}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	stream, err := js.Stream(ctx, broker.StreamName)
	if err != nil {
		t.Fatal("stream:", err)
	}
	raw, err := stream.GetLastMsgForSubject(ctx, "thump.decisions")
	if err != nil {
		t.Fatal("forced decision never reached thump.decisions:", err)
	}
	var got decision.Governed
	if err := wire.Unmarshal(raw.Data, &got); err != nil {
		t.Fatal("wire bytes didn't decode:", err)
	}
	if diff := cmp.Diff(decision.VerdictApproved, got.Decision.Verdict); diff != "" {
		t.Error("wrong verdict published (-want +got)", diff)
	}
	if !got.Decision.Forced {
		t.Error("want Forced=true on a break-glass decision")
	}
}

// wantTopUsage is calipers's own topUsage constant, copied rather than
// exported: if the two ever disagree, TestMain_ReturnsUsageErrorWithNoArgsExactly
// and TestMain_ReturnsUsageErrorForAnUndocumentedVerbExactly below fail
// immediately, which is the drift detector — not a second source of truth
// to keep in sync by hand.
const wantTopUsage = "usage: calipers <incidents|approve|force|unseal|corpus|rca|tune|replay|harvest|probe|transcript|scorecard|validate|step|pipeline|mock> [flags]\n"

// TestMain_RoutesEveryDocumentedVerbAndRefusesTheRest pins the usage string
// and the switch together. They drifted apart in trim already — the usage
// line named only "incidents" while four subcommands were routed — and a
// verb that exists but is undocumented is one nobody discovers. Every verb
// here is invoked with no further arguments and is expected to fail or
// succeed on its own terms (its own usage line, a clean skip, whatever) —
// the only thing this test cares about is that none of them fall through
// to the top-level default and print wantTopUsage, which is what "not
// wired into the switch" would look like.
func TestMain_RoutesEveryDocumentedVerbAndRefusesTheRest(t *testing.T) {
	// Not t.Parallel(): the corpus case needs t.Setenv, which panics
	// alongside a parallel subtest.
	t.Setenv("THUMP_SEAL_KEY", "") // corpus checks this before anything network-shaped
	t.Setenv("ANTHROPIC_API_KEY", "")

	for _, verb := range []string{"incidents", "approve", "force", "unseal", "corpus", "rca", "tune", "replay", "harvest", "probe", "transcript", "scorecard", "validate", "step", "pipeline", "mock"} {
		t.Run(verb, func(t *testing.T) {
			var out, errOut bytes.Buffer
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			calipers.MainContext(ctx, []string{verb}, &out, &errOut)

			if errOut.String() == wantTopUsage {
				t.Errorf("verb %q fell through to the default case — it is documented in topUsage but not routed in the switch", verb)
			}
		})
	}
}

func TestMain_ReturnsUsageErrorWithNoArgsExactly(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer

	code := calipers.Main(nil, &out, &errOut)

	if diff := cmp.Diff(2, code); diff != "" {
		t.Error("wrong exit code for no verb (-want +got)", diff)
	}
	if diff := cmp.Diff(wantTopUsage, errOut.String()); diff != "" {
		t.Error("wrong usage line for no verb (-want +got)", diff)
	}
}

func TestMain_ReturnsUsageErrorForAnUndocumentedVerbExactly(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer

	code := calipers.Main([]string{"polish"}, &out, &errOut)

	if diff := cmp.Diff(2, code); diff != "" {
		t.Error("wrong exit code for an undocumented verb (-want +got)", diff)
	}
	if diff := cmp.Diff(wantTopUsage, errOut.String()); diff != "" {
		t.Error("wrong usage line for an undocumented verb (-want +got)", diff)
	}
}

func TestMain_StepRattleRunsIsolatedRattleAndPrintsJSON(t *testing.T) {
	t.Parallel()
	watchPath := filepath.Join("..", "..", "config", "dev", "rattle", "watch.yaml")
	queryPath := filepath.Join("..", "..", "config", "dev", "rattle", "query.yaml")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Unix()
		query := r.URL.Query().Get("query")
		if query == `slo:current_burn_rate:ratio{sloth_id="cart-availability"}` {
			resp := fmt.Sprintf(`{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [{
						"metric": {"__name__": "slo:current_burn_rate:ratio", "sloth_id": "cart-availability"},
						"values": [
							[%d, "1.0"],
							[%d, "2.0"],
							[%d, "4.0"],
							[%d, "8.0"]
						]
					}]
				}
			}`, now-30, now-20, now-10, now)
			_, _ = w.Write([]byte(resp))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	t.Cleanup(srv.Close)

	var stdout, stderr bytes.Buffer
	code := calipers.MainContext(t.Context(), []string{
		"step", "rattle",
		"--watch", watchPath,
		"--query-config", queryPath,
		"--prom-url", srv.URL,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	var got signal.Detection
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (raw: %s)", err, stdout.String())
	}
	if diff := cmp.Diff("cart", got.OriginService); diff != "" {
		t.Errorf("wrong origin service (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("cart-availability", got.SLORef); diff != "" {
		t.Errorf("wrong sloRef (-want +got):\n%s", diff)
	}
}

func TestMain_StepHissRunsIsolatedHissAndPrintsJSON(t *testing.T) {
	t.Parallel()
	proposalPath := filepath.Join("..", "..", "test", "fixtures", "proposals", "cart-failure.json")
	policyPath := filepath.Join("..", "..", "config", "dev", "hiss", "policy.yaml")

	var stdout, stderr bytes.Buffer
	code := calipers.MainContext(t.Context(), []string{
		"step", "hiss",
		"--proposal", proposalPath,
		"--policy", policyPath,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	var got decision.Governed
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (raw: %s)", err, stdout.String())
	}
	if diff := cmp.Diff(decision.VerdictApproved, got.Decision.Verdict); diff != "" {
		t.Errorf("wrong decision verdict (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("slo_burn:cart", got.Decision.SignalRef); diff != "" {
		t.Errorf("wrong signalRef (-want +got):\n%s", diff)
	}
}

func TestMain_StepThumpRunsIsolatedThumpAndPrintsJSON(t *testing.T) {
	t.Parallel()
	decisionPath := filepath.Join("..", "..", "test", "fixtures", "decisions", "cart-failure.json")
	catalogPath := filepath.Join("..", "..", "config", "dev", "actions", "catalog.yaml")

	var stdout, stderr bytes.Buffer
	code := calipers.MainContext(t.Context(), []string{
		"step", "thump",
		"--decision", decisionPath,
		"--catalog", catalogPath,
		"--dry-run=true",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	var got outcome.Outcome
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (raw: %s)", err, stdout.String())
	}
	if diff := cmp.Diff(outcome.ModeDryRun, got.Mode); diff != "" {
		t.Errorf("wrong outcome mode (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(outcome.ResultRendered, got.Result); diff != "" {
		t.Errorf("wrong outcome result (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff("disable-cart-failure", got.ContractRef); diff != "" {
		t.Errorf("wrong contractRef (-want +got):\n%s", diff)
	}
}

func TestMain_StepUsageAndErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args            []string
		wantCode        int
		wantErrContains string
	}{
		"calipers step with no subverb prints usage and returns exit code 2": {
			args:            []string{"step"},
			wantCode:        2,
			wantErrContains: "usage: calipers step <rattle|clank|hiss|thump> [flags]",
		},
		"calipers step with unknown subverb prints usage and returns exit code 2": {
			args:            []string{"step", "frobnicate"},
			wantCode:        2,
			wantErrContains: "usage: calipers step <rattle|clank|hiss|thump> [flags]",
		},
		"calipers step hiss with missing proposal prints usage and returns exit code 2": {
			args:            []string{"step", "hiss"},
			wantCode:        2,
			wantErrContains: "usage: calipers step hiss",
		},
		"calipers step thump with missing decision prints usage and returns exit code 2": {
			args:            []string{"step", "thump"},
			wantCode:        2,
			wantErrContains: "usage: calipers step thump",
		},
		"calipers step clank with missing detection prints usage and returns exit code 2": {
			args:            []string{"step", "clank"},
			wantCode:        2,
			wantErrContains: "usage: calipers step clank",
		},
		"calipers step hiss with non-existent proposal returns exit code 1": {
			args:            []string{"step", "hiss", "--proposal", "nonexistent.json"},
			wantCode:        1,
			wantErrContains: "calipers:",
		},
		"calipers step thump with non-existent decision returns exit code 1": {
			args:            []string{"step", "thump", "--decision", "nonexistent.json"},
			wantCode:        1,
			wantErrContains: "calipers:",
		},
		"calipers step clank with nonexistent detection file returns exit code 1": {
			args:            []string{"step", "clank", "--detection", "nonexistent.json", "--profile", filepath.Join("..", "..", "config", "dev")},
			wantCode:        1,
			wantErrContains: "calipers:",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := calipers.MainContext(t.Context(), tc.args, &stdout, &stderr)

			if diff := cmp.Diff(tc.wantCode, code); diff != "" {
				t.Errorf("wrong exit code (-want +got):\n%s", diff)
			}
			if !strings.Contains(stderr.String(), tc.wantErrContains) {
				t.Errorf("stderr does not contain %q, got: %s", tc.wantErrContains, stderr.String())
			}
		})
	}
}

func TestMain_PipelineUsageAndErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args            []string
		wantCode        int
		wantErrContains string
	}{
		"calipers pipeline with no args prints usage and returns exit code 2": {
			args:            []string{"pipeline"},
			wantCode:        2,
			wantErrContains: "usage: calipers pipeline --detection <path>",
		},
		"calipers pipeline with nonexistent detection file returns exit code 1": {
			args:            []string{"pipeline", "--detection", "nonexistent.json"},
			wantCode:        1,
			wantErrContains: "calipers:",
		},
		"calipers pipeline with nonexistent profile directory returns exit code 1": {
			args:            []string{"pipeline", "--detection", filepath.Join("..", "..", "test", "fixtures", "detections", "cart-failure.json"), "--profile", "nonexistent-dir"},
			wantCode:        1,
			wantErrContains: "calipers:",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := calipers.MainContext(t.Context(), tc.args, &stdout, &stderr)

			if diff := cmp.Diff(tc.wantCode, code); diff != "" {
				t.Errorf("wrong exit code (-want +got):\n%s", diff)
			}
			if !strings.Contains(stderr.String(), tc.wantErrContains) {
				t.Errorf("stderr does not contain %q, got: %s", tc.wantErrContains, stderr.String())
			}
		})
	}
}

func TestMain_MockStartsTelemetryAndBroker(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)

	var stdout, stderr bytes.Buffer
	code := calipers.MainContext(ctx, []string{
		"mock",
		"--prom-port", "0",
		"--nats",
		"--nats-port", "-1",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "mock telemetry server listening on") {
		t.Errorf("stdout does not mention telemetry server, got: %s", out)
	}
	if !strings.Contains(out, "mock nats broker listening on") {
		t.Errorf("stdout does not mention nats broker, got: %s", out)
	}
}
