package clank

import (
	"context"
	"crypto/tls"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace/noop"
	"k8s.io/client-go/dynamic"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
)

// NewLoopForTest is the one deliberate crack in the package boundary: it lets
// clank_test build a loop through the exact same newLoop Main uses, without
// newLoop itself (or the unexported loop type it returns) becoming part of
// clank's real API. Only compiled under `go test` — the _test.go suffix means
// it never ships in the production binary. Tracing isn't what these tests are
// about, so it's wired to a noop.Tracer{} here rather than making every call
// site pass one.
func NewLoopForTest(model Model, tools map[string]Tool, intake *Intake, cat *contract.StaticCatalog, outbox, outcomes string, store Store) *loop {
	return newLoop("", outbox, outcomes, "", model, tools, intake, cat, shippedClasses(), store, time.Hour, noop.Tracer{}, nil, nil)
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
func NewBrokerEngineForTest(model Model, intake *Intake, store Store, tools map[string]Tool, cat *contract.StaticCatalog, pub publish.Publisher[proposal.Set], ledger *MemProposalLog, cases *CaseBase) *Engine {
	return newBrokerEngine(model, intake, store, tools, cat, shippedClasses(), pub, ledger, cases, time.Hour, noop.Tracer{}, nil, nil)
}

// TODO: These are a gooney workaround and this stuff should probably go elsewhere or be relagated to the dustbin of bad ideas.
func (l *MemProposalLog) SeedForTest(ps proposal.Set, age time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Backdate the record to simulate a stale entry
	l.sets = append(l.sets, recorded{set: ps, at: time.Now().Add(-age)})
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

func FromAnthropicForTest(resp *anthropic.Message) Completion {
	return fromAnthropicMessage(resp)
}

func ToAnthropicMessageParamsForTest(msgs []Message) []anthropic.MessageParam {
	return toAnthropicMessageParams(msgs)
}

func ToAnthropicToolParamsForTest(tools []ToolSpec) []anthropic.ToolUnionParam {
	return toAnthropicToolParams(tools)
}

// ToolSpecsForTest exposes Engine.toolSpecs, the one place tool order gets
// fixed before it's sent to the model — clank_test needs it to pin that
// order deterministic.
func ToolSpecsForTest(e *Engine) []ToolSpec {
	return e.toolSpecs()
}

// BuildIntakeForTest exposes buildIntake to clank_test — the seam W0b's
// silent-fallback warnings hang off, and where W1's ArgoCD change source
// plugs in next.
func BuildIntakeForTest(cfg config.Clank, backendTLS *tls.Config, argo dynamic.Interface) (*Intake, error) {
	return buildIntake(cfg, backendTLS, argo)
}

// IntakeTopologyForTest exposes the Intake's wired TopologySource.
func IntakeTopologyForTest(i *Intake) TopologySource {
	return i.topo
}

// IntakeChangeForTest exposes the Intake's wired ChangeSource.
func IntakeChangeForTest(i *Intake) ChangeSource {
	return i.change
}

func NewIdentifierMaskerForTest() *identifierMasker { return newIdentifierMasker() }

func (m *identifierMasker) RegisterForTest(name string)   { m.register(name) }
func (m *identifierMasker) MaskForTest(s string) string   { return m.mask(s) }
func (m *identifierMasker) UnmaskForTest(s string) string { return m.unmask(s) }

func NewMaskingModelForTest(model Model, mask *identifierMasker) Model {
	return &maskingModel{Model: model, mask: mask}
}

func ContextWithMaskerForTest(ctx context.Context, mask *identifierMasker) context.Context {
	return contextWithMasker(ctx, mask)
}
