package harvest

import (
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/configfile"
)

// ErrInvalidScenario means a row in the scenario table failed
// validation.
var ErrInvalidScenario = errors.New("harvest: scenario table is invalid")

// Scenario is one calibration datum's worth of work.
type Scenario struct {
	Name   string
	Domain string
	// SignalRef is the fingerprint rattle emits when this fault fires —
	// authored, never composed here, because rattle owns the rule that builds
	// it and a second copy would drift. Settle matches outcomes on this and
	// nothing else, so a wrong value costs the whole settle window and
	// reports as a timeout the rig never earned.
	SignalRef     string
	Fault         Action
	Preconditions []Precondition
	Expects       Expects
	SettleWindow  time.Duration
	Restore       Action
}

// Action is one thing the harvest does to the cluster: apply a fault
// manifest, exec a script, or delete/reverse what a prior Action applied.
type Action struct {
	Path  string
	Apply string
	Args  []string
}

// Precondition is set before the fault fires and its Restore run
// after, in reverse declaration order.
type Precondition struct {
	Name    string
	Set     string
	Restore string
}

// Expects is what the settled outcome is graded against.  Verdict is one
// of "approved", "held", "declined".
type Expects struct {
	FailureClass proposal.FailureClass
	ContractRef  string
	Verdict      string
}

var validApply = map[string]bool{
	"kubectl":        true,
	"kubectl-delete": true,
	"exec":           true,
}

var validVerdict = map[string]bool{
	"approved": true,
	"held":     true,
	"declined": true,
}

// --- staging shape: mirrors chaos/scenarios.yaml's keys one-for-one ---

type scenariosFile struct {
	Version   int             `json:"version"`
	Rig       string          `json:"rig"`
	Scenarios []scenarioStage `json:"scenarios"`
}

type scenarioStage struct {
	Name          string              `json:"name"`
	Domain        string              `json:"domain"`
	SignalRef     string              `json:"signalRef"`
	Fault         actionStage         `json:"fault"`
	Preconditions []preconditionStage `json:"preconditions"`
	Expects       expectsStage        `json:"expects"`
	SettleWindow  string              `json:"settleWindow"`
	Restore       actionStage         `json:"restore"`
}

func (s scenarioStage) validate() (Scenario, error) {
	if s.Name == "" {
		return Scenario{}, errors.New("scenario has no name")
	}
	fault, err := s.Fault.validate()
	if err != nil {
		return Scenario{}, fmt.Errorf("fault: %w", err)
	}
	restore, err := s.Restore.validate()
	if err != nil {
		return Scenario{}, fmt.Errorf("restore: %w", err)
	}
	if restore.Path == "" {
		return Scenario{}, errors.New("no restore - a harvest that cannot restore is a rig teardown")
	}
	if !validVerdict[s.Expects.Verdict] {
		return Scenario{}, fmt.Errorf("expects.verdict %q not none of approved, held, declined", s.Expects.Verdict)
	}
	if s.Expects.ContractRef == "" {
		return Scenario{}, errors.New("expects.contractRef is empty")
	}
	if s.SignalRef == "" {
		return Scenario{}, errors.New("no signalRef - a scenario that names no signal waits out its whole window on an outcome that can never match")
	}
	window, err := time.ParseDuration(s.SettleWindow)
	if err != nil {
		return Scenario{}, fmt.Errorf("settleWindow %q: %w", s.SettleWindow, err)
	}
	if window <= 0 {
		return Scenario{}, fmt.Errorf("settleWindow %q must be positive", s.SettleWindow)
	}

	preconditions := make([]Precondition, 0, len(s.Preconditions))
	for _, p := range s.Preconditions {
		if p.Set == "" || p.Restore == "" {
			return Scenario{}, fmt.Errorf("precondition %q must have both set and restore", p.Name)
		}
		preconditions = append(preconditions, Precondition(p))
	}

	return Scenario{
		Name:          s.Name,
		Domain:        s.Domain,
		SignalRef:     s.SignalRef,
		Fault:         fault,
		Preconditions: preconditions,
		Expects: Expects{
			FailureClass: proposal.FailureClass(s.Expects.FailureClass),
			ContractRef:  s.Expects.ContractRef,
			Verdict:      s.Expects.Verdict,
		},
		SettleWindow: window,
		Restore:      restore,
	}, nil
}

type actionStage struct {
	Path  string   `json:"path"`
	Apply string   `json:"apply"`
	Args  []string `json:"args"`
}

func (a actionStage) validate() (Action, error) {
	if a.Path == "" {
		return Action{}, errors.New("path is empty")
	}
	if !validApply[a.Apply] {
		return Action{}, fmt.Errorf("apply %q not one of kubectl, kubectl-delete, exec", a.Apply)
	}
	return Action(a), nil
}

type preconditionStage struct {
	Name    string `json:"name"`
	Set     string `json:"set"`
	Restore string `json:"restore"`
}

type expectsStage struct {
	FailureClass string `json:"failureClass"`
	ContractRef  string `json:"contractRef"`
	Verdict      string `json:"verdict"`
}

// Table is a scenario file's whole contents: the rows plus the rig they were
// authored against. The rig travels with them because a signalRef is only
// checkable against one rig's watch list — a bare []Scenario has thrown away
// what it would have to be validated against.
type Table struct {
	Rig       string
	Scenarios []Scenario
}

// LoadScenarios reads path and validates it into a Table.
func LoadScenarios(path string) (Table, error) {
	sf, err := configfile.Stage[scenariosFile](path, "scenario table")
	if err != nil {
		return Table{}, err
	}
	if sf.Rig == "" {
		return Table{}, fmt.Errorf("%w: no rig - a signalRef is only checkable against one rig's watch list", ErrInvalidScenario)
	}

	out := make([]Scenario, 0, len(sf.Scenarios))
	for _, s := range sf.Scenarios {
		sc, err := s.validate()
		if err != nil {
			return Table{}, fmt.Errorf("%w: scenario %q: %w", ErrInvalidScenario, s.Name, err)
		}
		out = append(out, sc)
	}

	return Table{Rig: sf.Rig, Scenarios: out}, nil
}
