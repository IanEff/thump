package clank

import (
	"context"
	"crypto/tls"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/subjects"
	"github.com/nats-io/nats.go/jetstream"
)

// RebuildLedgerForTest exposes rebuildLedger to clank_test — the replay
// logic itself, independent of the composition root that wires it in.
func RebuildLedgerForTest(ctx context.Context, js jetstream.JetStream, retention time.Duration, cases *CaseBase) (*MemProposalLog, error) {
	return rebuildLedger(ctx, js, retention, cases)
}

// BuildLedgerForTest exposes buildLedger to clank_test — the composition-root
// seam runBroker actually calls, so a wiring-guard test can prove that call
// site reaches replay rather than a bare NewMemProposalLog() a future edit
// could silently swap back in.
func BuildLedgerForTest(ctx context.Context, js jetstream.JetStream, retention time.Duration, cases *CaseBase) (*MemProposalLog, error) {
	return buildLedger(ctx, js, retention, cases)
}

// NewLoopForTest is the one deliberate crack in the package boundary: it lets
// clank_test build a loop through the exact same newLoop Main uses, without
// newLoop itself (or the unexported loop type it returns) becoming part of
// clank's real API. Only compiled under `go test` — the _test.go suffix means
// it never ships in the production binary. Tracing isn't what these tests are
// about, so it's wired to a noop.Tracer{} here rather than making every call
// site pass one.
func NewLoopForTest(model reason.Model, tools map[string]reason.Tool, intake *Intake, cat *contract.StaticCatalog, outbox, outcomes string, store Store) *loop {
	return newLoop("", outbox, outcomes, "", model, tools, intake, cat, shippedClasses(), store, time.Hour, noop.Tracer{}, nil, nil, DefaultScoringWeights(), DefaultLimits())
}

// ShippedCatalogForTest exposes the production catalog (the one Main wires)
// to clank_test, so the golden-path suite proves the loop against the SAME
// actions clank actually ships — not a bespoke test catalog that begs the
// question. Test-only, like the rest of this file.
func ShippedCatalogForTest() *contract.StaticCatalog {
	return shippedCatalog()
}

// ShippedFailureClassesForTest exposes the class definitions seedPrompt
// renders, for the same reason ShippedCatalogForTest exposes the catalog: a
// test that asserts against its own list proves only that the list agrees
// with itself.
func ShippedFailureClassesForTest() []contract.FailureClassDefinition {
	return shippedClasses()
}

// NewBrokerEngineForTest exposes the broker-mode Engine construction to tests.
func NewBrokerEngineForTest(model reason.Model, intake *Intake, store Store, tools map[string]reason.Tool, cat *contract.StaticCatalog, pub publish.Publisher[proposal.Set], ledger *MemProposalLog, cases *CaseBase) *Engine {
	return newBrokerEngine(model, intake, store, tools, cat, shippedClasses(), pub, ledger, cases, time.Hour, noop.Tracer{}, nil, nil, DefaultScoringWeights(), DefaultLimits())
}

// SeedForTest backdates ps into the ledger as if Record had run age ago —
// a thin wrapper over seedAt, the same primitive rebuildLedger uses to
// replay history with its real timestamps.
func (l *MemProposalLog) SeedForTest(ps proposal.Set, age time.Duration) {
	l.seedAt(ps, time.Now().Add(-age))
}

func (l *MemProposalLog) LenForTest() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.sets)
}

func (cb *CaseBase) SetCasesForTest(cases []Case) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.cases = cases
}

// ScoreConfidenceForTest exposes scoreConfidence to clank_test with the
// grounding tiers this build settled on — 0.3 uncorroborated, 0.7 for one
// citation, 1.0 for two or more — the same table that locks those
// coefficients as a regression, not a tunable default.
func ScoreConfidenceForTest(signalConf float64, corroborated int, selfReported float64) float64 {
	return scoreConfidence(confidenceInputs{
		SignalConfidence: signalConf,
		Corroborated:     corroborated,
		SelfReported:     selfReported,
	}, DefaultScoringWeights())
}

// ScoreConfidencesForTest exposes scoreConfidences to clank_test — the
// entry point Propose calls once per run, letting a test drive the causal
// Likelihood term through the same code path production uses instead of
// reimplementing scoreConfidence's per-candidate loop.
func ScoreConfidencesForTest(set *proposal.Set, sao proposal.SAO, prior Prior, fingerprint string, w ScoringWeights) {
	scoreConfidences(set, sao, prior, fingerprint, w)
}

// CoherentLiveCitationsForTest exposes coherentLiveCitations to clank_test —
// the corroboration count scoreConfidences feeds into the grounding tiers,
// so a test can pin what counts as coherent without going through a full
// Propose run.
func CoherentLiveCitationsForTest(cand proposal.Candidate, evidence []proposal.EvidenceRef, sao *proposal.SAO) int {
	return coherentLiveCitations(cand, evidence, sao)
}

// shippedCatalog and shippedClasses load the config the production binary
// loads, so the suite proves the loop against the actions clank actually
// ships rather than a bespoke fixture that begs the question. A failure here
// is a broken checked-in config, not a test condition — it panics rather
// than handing every caller an error it can't act on.
var shippedCatalog = sync.OnceValue(func() *contract.StaticCatalog {
	cat, err := contract.LoadCatalogFile(
		filepath.Join("..", "..", "config", "actions", "catalog.yaml"),
		contract.Preconditions,
	)
	if err != nil {
		panic("clank test kit: " + err.Error())
	}
	return cat
})

var shippedClasses = sync.OnceValue(func() []contract.FailureClassDefinition {
	defs, err := contract.LoadFailureClassesFile(
		filepath.Join("..", "..", "config", "actions", "failure-classes.yaml"),
	)
	if err != nil {
		panic("clank test kit: " + err.Error())
	}
	return defs
})

// ToolSpecsForTest exposes Engine.toolSpecs, the one place tool order gets
// fixed before it's sent to the model — clank_test needs it to pin that
// order deterministic.
func ToolSpecsForTest(e *Engine) []reason.ToolSpec {
	return e.toolSpecs()
}

// BuildIntakeForTest exposes buildIntake to clank_test — the seam the
// silent-fallback warnings hang off, and where the ArgoCD change source and
// its subject rules are wired together.
func BuildIntakeForTest(cfg config.Clank, backendTLS *tls.Config, argo dynamic.Interface, subjects subjects.SubjectIndex, changeLookback time.Duration) (*Intake, error) {
	return buildIntake(cfg, backendTLS, argo, subjects, changeLookback)
}

// BuildToolsForTest exposes buildTools to clank_test — the seam that decides
// which evidence tools exist and whether each one can name the topology node
// its citations concern.
func BuildToolsForTest(cfg config.Clank, backendTLS *tls.Config, ev evidence.Config, kube kubernetes.Interface) map[string]reason.Tool {
	return buildTools(cfg, backendTLS, ev, kube)
}

// ClientsForTest exposes clientsFor to clank_test — the half of the
// in-cluster path that doesn't need a cluster, so the nil-on-failure contract
// its callers' nil checks depend on can actually be exercised.
func ClientsForTest(restConfig *rest.Config) (kubernetes.Interface, dynamic.Interface) {
	return clientsFor(restConfig)
}

// IntakeTopologyForTest exposes the Intake's wired reason.TopologySource.
func IntakeTopologyForTest(i *Intake) reason.TopologySource {
	return i.topo
}

// IntakeChangeForTest exposes the Intake's wired reason.ChangeSource.
func IntakeChangeForTest(i *Intake) reason.ChangeSource {
	return i.change
}

// ModelRequestTimeoutForTest exposes the bound on one model call — the only
// handler timeout in the tree that deliberately exceeds AckWait.
func ModelRequestTimeoutForTest() time.Duration { return modelRequestTimeout }

// RecordResolutionForTest exposes Recorder.recordResolution to clank_test.
func (r *Recorder) RecordResolutionForTest(set proposal.Set, o outcome.Outcome) {
	r.recordResolution(set, o)
}

// RecordCalibrationForTest exposes Recorder.recordCalibration to clank_test.
func (r *Recorder) RecordCalibrationForTest(set proposal.Set, o outcome.Outcome) {
	r.recordCalibration(set, o)
}

// RecordEffectivenessForTest exposes Recorder.recordEffectiveness to clank_test.
func (r *Recorder) RecordEffectivenessForTest(set proposal.Set, o outcome.Outcome) {
	r.recordEffectiveness(set, o)
}

// CalibrationCollectorForTest exposes the calibration counter as a
// prometheus.Collector, so clank_test can read it with testutil without
// reaching into the unexported field directly.
func (r *Recorder) CalibrationCollectorForTest() prometheus.Collector {
	return r.calibration
}

// ConfidenceCollectorForTest exposes the confidence histogram as a
// prometheus.Collector, mirroring CalibrationCollectorForTest.
func (r *Recorder) ConfidenceCollectorForTest() prometheus.Collector {
	return r.confidence
}

// ConfidenceBucketForTest exposes confidenceBucket to clank_test.
func ConfidenceBucketForTest(conf float64) string {
	return confidenceBucket(conf)
}
