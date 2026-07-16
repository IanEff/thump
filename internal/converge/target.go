package converge

import (
	"fmt"
	"regexp"
	"strconv"
)

// thresholdRe pulls the numeric threshold out of a "< N" comparison in a
// SuccessCriteria.Target prose string ("p99 < 250ms", "avg < 50ms"). Only
// "<" is authored today (authored.go) — anything else is refused, not
// guessed at.
var thresholdRe = regexp.MustCompile(`<\s*([\d.]+)`)

// parseTarget turns target's prose into a comparator ("<" or, for the
// "HEALTH_OK" special case, "==") and the threshold to compare a metric's
// live value against. An unrecognized shape errors rather than silently
// falling back to some default comparison — a Converger that guesses at an
// operator it can't parse is worse than one that refuses to answer.
func parseTarget(target string) (op string, threshold float64, err error) {
	if target == "HEALTH_OK" {
		return "==", 0, nil
	}
	m := thresholdRe.FindStringSubmatch(target)
	if m == nil {
		return "", 0, fmt.Errorf("converge: cannot parse target %q", target)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return "", 0, fmt.Errorf("converge: cannot parse target %q: %w", target, err)
	}
	return "<", f, nil
}
