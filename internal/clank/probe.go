package clank

import (
	"crypto/tls"
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/reason"
)

// ProbeEngine builds a real Engine from cfg through the same buildTools and
// buildIntake composition Main wires into production — the read-only tools,
// the topology and change sources, the shipped catalog — with no Pub and no
// Journal set. A probe run can never reach the broker or the corpus, by
// construction, not by policy: there is nothing here to leave unpublished.
//
// Ledger and Store are left for ProbeReset, not set here — call it before
// every Propose call the caller makes, including the first.
func ProbeEngine(cfg config.Clank, backendTLS *tls.Config, ev evidence.Config, kube kubernetes.Interface, argo dynamic.Interface, cat *contract.StaticCatalog, classes []contract.FailureClassDefinition, model reason.Model, weights ScoringWeights, limits Limits) (*Engine, error) {
	intake, err := buildIntake(cfg, backendTLS, kube, argo, ev.Index, limits.ChangeLookback)
	if err != nil {
		return nil, fmt.Errorf("build intake: %w", err)
	}

	cases := NewCaseBase()
	cases.MaxCases = limits.MaxCases

	return &Engine{
		Intake:         intake,
		Model:          model,
		Tools:          buildTools(cfg, backendTLS, ev, kube),
		Catalog:        cat,
		FailureClasses: classes,
		Ranker:         NewRanker(),
		Gate:           ReadinessGate{},
		Scorer:         &CausalScorerImpl{Prior: cases},
		Prior:          cases,
		DedupeWindow:   cfg.DedupeWindow,
		MaxSteps:       limits.MaxSteps,
		Weights:        weights,
	}, nil
}

// ProbeReset gives e a fresh, empty Ledger and Store. A probe run calls this
// before every Propose against the same detection — sharing one Ledger
// across repeated runs would let run 2 see run 1's own set as an open dupe
// and gate-fail it on dedup, corrupting the exact variance a probe exists to
// measure. Both are in-memory and discarded when the caller moves on: a
// probe never durably records a run.
func ProbeReset(e *Engine) {
	e.Ledger = NewMemProposalLog()
	e.Store = NewMemStore()
}
