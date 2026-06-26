// Package clank is the Reasoning Plane: it turns one Signal into a recorded,
// deduplicated, evidence-backed Proposal. It selects; it does not permit, detect,
// or touch infrastructure.
package clank

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Signal is the input. It's rattle's output, consumed read-only and trusted:
// clank never recomputes the fingerprint or re-judges the signal.
type Signal struct {
	ID          string
	Fingerprint string  // de-dupe key, assigned by rattle
	Contract    string  // contract name + version, e.g. "slo_burn:v1"
	Kind        string  // "slo_burn", "pod_crashloop", ...
	Summary     string  // human one-liner
	Confidence  float64 // signal-level confidence; rattle's, read-only here
	DetectedAt  time.Time
}

// EvidenceRef points to evidence; it never carries the evidence itself. There is
// no raw field by design (Invariant 1).
type EvidenceRef struct {
	Tool    string // tool that produced it
	Query   string // what was asked
	Summary string // the digest, one line
	Ref     string // back-end ID to re-fetch raw later
}

// Decision is the Candidate Action handed across the Reasoning→Governance seam.
// It's not yet a proposal (the Gate hasn't judged it) and never authorized.
type Decision struct {
	RunID       string
	Fingerprint string
	Action      string
	Args        json.RawMessage

	Hypothesis   string
	Alternatives []Hypothesis
	Evidence     []EvidenceRef
	Confidence   float64 // candidate-action confidence; distinct from Signal.Confidence, never gated on
	// RequestedAuthorityLevel is a request, not a verdict; Governance converts it.
	RequestedAuthorityLevel string
	Digest                  string
}

// Hypothesis is one candidate explanation with the reasoner's weight on it.
type Hypothesis struct {
	Name   string
	Weight float64
}

// Status is the closed vocabulary for how a decision ended.
type Status string

const (
	StatusProposed             Status = "proposed"              // admitted; a real Proposal
	StatusSuppressedDuplicate  Status = "suppressed_duplicate"  // gate found one already open
	StatusInsufficientEvidence Status = "insufficient_evidence" // gate found no evidence
	StatusBudgetExhausted      Status = "budget_exhausted"      // loop hit MaxSteps
	StatusNoAction             Status = "no_action"             // model declined to propose
)

// Outcome is the recorded result of one Signal; Status==StatusProposed means it's
// a Proposal. This is what lands in the ledger.
type Outcome struct {
	Decision Decision
	Status   Status
	Reason   string // human explanation
	At       time.Time
}

// Gate decides whether a Decision is worth emitting: no evidence → reject, open
// dupe → suppress, else admit. Admit means "emit", not "authorized" — no policy.
type Gate interface {
	Evaluate(d Decision, openDupes []Outcome) Verdict
}

type Verdict struct {
	Admit  bool
	Status Status
	Reason string
}

// ReadinessGate is the one real Gate implementation.
type ReadinessGate struct{}

func (g ReadinessGate) Evaluate(d Decision, openDupes []Outcome) Verdict {
	if len(d.Evidence) == 0 {
		return Verdict{Admit: false, Status: StatusInsufficientEvidence, Reason: "no evidence"}
	}
	if len(openDupes) > 0 {
		return Verdict{Admit: false, Status: StatusSuppressedDuplicate, Reason: "open proposal exists"}
	}
	return Verdict{Admit: true, Status: StatusProposed}
}

// Store checkpoints each turn so a crashed run can resume. In-memory for now.
type Store interface {
	Checkpoint(ctx context.Context, t Turn) error
	Pending(ctx context.Context) ([]Turn, error)
	Finish(ctx context.Context, runID string, runErr error) error
}

type Message struct {
	Role    string // "system" | "user" | "assistant"
	Content string // text or digest, never raw (Invariant 1)
}

// Turn is one checkpointed step: the conversation so far, kept small (re-sent each turn).
type Turn struct {
	RunID string
	Step  int
	Msgs  []Message
}

// ProposalLog is the ledger of outcomes and the source of dedup truth.
type ProposalLog interface {
	Record(ctx context.Context, o Outcome) error
	// Open returns open proposals for this fingerprint since the given time.
	Open(ctx context.Context, fingerprint string, since time.Time) ([]Outcome, error)
}

// Model is the LLM seam. Real impl calls a provider; the test impl is scripted.
type Model interface {
	Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error)
}

type Completion struct {
	Message   Message
	ToolCalls []ToolCall // what the model wants to call this turn; empty means done
}

// ToolSpec describes a tool to the model. Shape is provisional.
type ToolSpec struct {
	Name        string
	Description string
}

type ToolCall struct {
	Name string
	Args json.RawMessage
}

// Tool is a read-only telemetry query: query in, digest out. EvidenceRef has no
// raw field, so a Tool can't hand back raw data.
type Tool interface {
	Name() string
	Spec() ToolSpec
	Run(ctx context.Context, args json.RawMessage) (EvidenceRef, error)
}

// ProposalSink is how an admitted proposal leaves the system.
type ProposalSink interface {
	Deliver(ctx context.Context, o Outcome) error
}

// Engine ties it together. This is what you call.
type Engine struct {
	Model     Model
	Store     Store
	Proposals ProposalLog
	Gate      Gate
	Sink      ProposalSink
	Tools     []Tool
	MaxSteps  int
}

// Propose runs the reason loop for one Signal and returns the recorded Outcome.
func (e *Engine) Propose(ctx context.Context, sig Signal) (Outcome, error) {
	// STEP 1 — Seed the conversation: build the opening []Message from the Signal.
	msgs := []Message{{Role: "user", Content: sig.Summary}}

	// STEP 2 — The bounded loop (Invariant 2): for step := 0; step < e.MaxSteps; step++
	//   a) Ask the model:        e.Model.Complete(ctx, msgs, ...)
	//   b) Checkpoint the turn:  e.Store.Checkpoint(...)   (Invariant 5)
	//   c) switch on each completion.ToolCalls[i].Name:
	//        "propose"  -> Unmarshal the Decision, copy sig.Fingerprint onto it,
	//                      ask e.Proposals.Open for dupes, e.Gate.Evaluate, build the
	//                      Outcome, e.Proposals.Record it, e.Sink.Deliver if admitted, return.
	//        default    -> find the Tool whose Name() matches, Run it, append the
	//                      EvidenceRef.Summary digest to msgs, loop again.
	for step := 0; step < e.MaxSteps; step++ {
		completion, err := e.Model.Complete(ctx, msgs, nil)
		if err != nil {
			return Outcome{}, err
		}
		if err := e.Store.Checkpoint(ctx, Turn{RunID: sig.ID, Step: step, Msgs: msgs}); err != nil {
			return Outcome{}, err
		}

		for _, call := range completion.ToolCalls {
			switch call.Name {
			case "insufficient":
				return Outcome{Status: StatusNoAction}, nil
			case "propose":
				// model is done investigating
				var d Decision
				if err := json.Unmarshal(call.Args, &d); err != nil {
					return Outcome{}, err
				}
				d.Fingerprint = sig.Fingerprint

				openDupes, err := e.Proposals.Open(ctx, d.Fingerprint, time.Now().Add(-time.Hour))
				if err != nil {
					return Outcome{}, err
				}
				verdict := e.Gate.Evaluate(d, openDupes)
				out := Outcome{Decision: d, Status: verdict.Status, Reason: verdict.Reason, At: time.Now()}
				if err := e.Proposals.Record(ctx, out); err != nil {
					return Outcome{}, err
				}
				if verdict.Admit {
					if err := e.Sink.Deliver(ctx, out); err != nil {
						return Outcome{}, err
					}
				}
				return out, nil

			default:
				for _, tool := range e.Tools {
					if tool.Name() == call.Name {
						ev, err := tool.Run(ctx, call.Args)
						if err != nil {
							return Outcome{}, err
						}
						msgs = append(msgs, Message{Role: "user", Content: ev.Summary})
					}
				}
			}
		}
	}

	// STEP 3 — Fell out of the loop without proposing -> budget_exhausted (Invariant 2).

	return Outcome{Status: StatusBudgetExhausted, At: time.Now()}, nil
}

type MemProposalLog struct {
	byFingerprint map[string][]Outcome
}

func NewMemProposalLog() *MemProposalLog {
	return &MemProposalLog{byFingerprint: map[string][]Outcome{}}
}

func (l *MemProposalLog) Record(_ context.Context, o Outcome) error {
	fp := o.Decision.Fingerprint
	l.byFingerprint[fp] = append(l.byFingerprint[fp], o)
	return nil
}

func (l *MemProposalLog) Open(_ context.Context, fingerprint string, since time.Time) ([]Outcome, error) {
	var open []Outcome
	for _, o := range l.byFingerprint[fingerprint] {
		if o.At.After(since) {
			open = append(open, o)
		}
	}
	return open, nil
}

// MemStore is the in-memory Store: turns keyed by RunID, finished runs dropped.
type MemStore struct {
	mu      sync.RWMutex
	byRunID map[string][]Turn
}

// Compile-time check that *MemStore satisfies the Store seam.
var _ Store = (*MemStore)(nil)

func NewMemStore() *MemStore {
	return &MemStore{byRunID: map[string][]Turn{}}
}

// Checkpoint saves one turn so a crashed run can resume.
func (s *MemStore) Checkpoint(_ context.Context, t Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byRunID[t.RunID] = append(s.byRunID[t.RunID], t)
	return nil
}

// Pending returns every turn for runs that haven't been finished yet.
func (s *MemStore) Pending(_ context.Context) ([]Turn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var turns []Turn
	for _, ts := range s.byRunID {
		turns = append(turns, ts...)
	}
	return turns, nil
}

// Finish marks a run done by dropping its turns; it's then no longer pending.
func (s *MemStore) Finish(_ context.Context, runID string, _ error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byRunID, runID)
	return nil
}
