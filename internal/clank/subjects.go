package clank

// SubjectRule maps the coordinates of a log or cluster query to the one
// catalog-info.yaml entity it is evidence about — authored per rig, because
// which namespace and labels belong to which topology node is a property of
// someone's deployment, never of this engine.
type SubjectRule struct {
	Subject   string            `json:"subject"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"` // every key must match for the rule to apply; more keys means more specific
}

// SubjectIndex resolves a query's coordinates to the topology node
// EvidenceRef.Subject declares. The rules are authored, never model-supplied:
// a citation that labels itself would be the model certifying the coherence
// check the gate applies to it.
type SubjectIndex []SubjectRule

// For returns the subject of the most specific rule matching namespace and
// every label it names, or "" when none matches or two equally specific rules
// disagree — a query two nodes both claim has made no claim, and an invented
// subject is worse than an absent one, since the gate trusts a tag it can
// match against the frozen topology.
func (x SubjectIndex) For(namespace string, labels map[string]string) string {
	subject, specificity, ambiguous := "", -1, false
	for _, rule := range x {
		if rule.Namespace != namespace || !labelsCover(rule.Labels, labels) {
			continue
		}
		switch {
		case len(rule.Labels) > specificity:
			subject, specificity, ambiguous = rule.Subject, len(rule.Labels), false
		case len(rule.Labels) == specificity && rule.Subject != subject:
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return subject
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
