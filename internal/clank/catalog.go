package clank

import "time"

type StaticCatalog struct {
	contracts []ActionContract
}

func (s *StaticCatalog) Applicable(class FailureClass, tier string, sao SAO) []ActionContract {
	var applicableContracts []ActionContract
	for _, c := range s.contracts {
		if !classMatches(c, class) {
			continue
		}
		if !tierMatches(c, tier) {
			continue
		}
		if !preconditionsMet(c, sao) {
			continue
		}
		applicableContracts = append(applicableContracts, c)
	}
	return applicableContracts
}

func classMatches(c ActionContract, class FailureClass) bool {
	for _, fc := range c.ApplicableFailureClasses {
		if fc == class {
			return true
		}
	}
	return false
}

func tierMatches(c ActionContract, tier string) bool {
	for _, t := range c.ApplicableTiers {
		if t == tier {
			return true
		}
	}
	return false
}

func preconditionsMet(c ActionContract, sao SAO) bool {
	for _, p := range c.Preconditions {
		if !p.OK(sao) {
			return false
		}
	}
	return true
}

func NewStaticCatalog(t []ActionContract) *StaticCatalog {
	return &StaticCatalog{contracts: t}
}

type ActionContract struct {
	Name                     string
	ApplicableFailureClasses []FailureClass
	ApplicableTiers          []string
	Preconditions            []Precondition
	Action                   ActionSpec
	Reversal                 Reversal
	SuccessCriteria          SuccessCriteria
}

type ActionSpec struct {
	Description     string
	ScopeParameters map[string]Range
}

type Range struct {
	Min     float64
	Max     float64
	Default float64
}

type Reversal struct {
	Method   string
	Fallback string
}

type SuccessCriteria struct {
	Metric          string
	Target          string
	Window          time.Duration
	AbortConditions []string
}

type Precondition struct {
	Name string
	OK   func(SAO) bool
}
