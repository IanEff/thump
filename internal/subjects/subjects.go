// Package subjects resolves cluster coordinates to a topology node — the one
// index both the evidence plane and the change plane consult so that a
// citation and a change event name a subject the same way.
package subjects

import "sort"

// Coordinates are the cluster facts a caller can state about one piece of
// evidence or one change event. Callers know different subsets — a log query
// knows a namespace and stream labels, an ArgoCD resource entry knows a
// namespace, kind and name — so an empty field is "not stated", never "must be
// empty".
type Coordinates struct {
	Namespace string            `json:"namespace,omitempty"`
	Kind      string            `json:"kind,omitempty"` // a Kubernetes kind as ArgoCD reports it in an Application's resource inventory
	Name      string            `json:"name,omitempty"` // the object name in that inventory, which is rarely the topology node's own name
	Labels    map[string]string `json:"labels,omitempty"`
}

// SubjectRule maps the coordinates of a query, or of a changed resource, to the
// one catalog-info.yaml entity it concerns — authored per rig, because which
// namespace, workload and labels belong to which topology node is a property of
// someone's deployment, never of this engine. A rule constrains only the
// coordinates it declares.
type SubjectRule struct {
	Subject string `json:"subject"`
	Coordinates
}

// SubjectIndex resolves cluster coordinates to the topology node an
// EvidenceRef.Subject or a ChangeEvent.Target names. The rules are authored,
// never model-supplied: a citation that labels itself would be the model
// certifying the coherence check the gate applies to it. Both the evidence
// plane and the change plane resolve through this one index, so a name the
// graph does not know fails the same way on both.
type SubjectIndex []SubjectRule

// For returns the subject of the most specific rule matching c, or "" when none
// matches or two equally specific rules disagree — coordinates two nodes both
// claim have made no claim, and an invented subject is worse than an absent
// one, since the gate trusts a tag it can match against the frozen topology.
// Specificity is the number of coordinates a rule constrains, so a rule naming
// a namespace and a resource name beats one naming the namespace alone.
func (x SubjectIndex) For(c Coordinates) string {
	subject, specificity, ambiguous := "", -1, false
	for _, rule := range x {
		if !rule.matches(c) {
			continue
		}
		switch n := rule.constraints(); {
		case n > specificity:
			subject, specificity, ambiguous = rule.Subject, n, false
		case n == specificity && rule.Subject != subject:
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return subject
}

// LabelKeys returns every label key declared across x's rules, sorted and de-
// duplicate.
func (x SubjectIndex) LabelKeys() []string {
	seen := make(map[string]any)
	for _, rule := range x {
		for k := range rule.Labels {
			seen[k] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ExampleLabel returns one label key/value pair from the first rule in x
// that declares any — a tool description's worked example, drawn from what
// the rig actually authored rather than invented. ok is false when no rule
// declares a label.
func (x SubjectIndex) ExampleLabel() (key, value string, ok bool) {
	for _, rule := range x {
		if len(rule.Labels) == 0 {
			continue
		}
		keys := make([]string, 0, len(rule.Labels))
		for k := range rule.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys[0], rule.Labels[keys[0]], true
	}
	return "", "", false
}

// matches reports whether c satisfies every coordinate rule declares. An
// undeclared coordinate is unconstrained, so a namespace-wide rule still claims
// a query that also names a kind.
func (rule SubjectRule) matches(c Coordinates) bool {
	if rule.Namespace != "" && rule.Namespace != c.Namespace {
		return false
	}
	if rule.Kind != "" && rule.Kind != c.Kind {
		return false
	}
	if rule.Name != "" && rule.Name != c.Name {
		return false
	}
	return labelsCover(rule.Labels, c.Labels)
}

// constraints counts the coordinates rule pins, which is what ranks two
// matching rules against each other.
func (rule SubjectRule) constraints() int {
	n := len(rule.Labels)
	for _, field := range []string{rule.Namespace, rule.Kind, rule.Name} {
		if field != "" {
			n++
		}
	}
	return n
}

// labelsCover reports whether query carries every key in want at the same
// value — a rule constrains the query, so extra labels on the query never
// disqualify it.
func labelsCover(want, query map[string]string) bool {
	for k, v := range want {
		if query[k] != v {
			return false
		}
	}
	return true
}
