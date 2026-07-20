package converge

import (
	"fmt"
	"regexp"
	"strconv"
)

// comparisonRe pulls the operator and numeric threshold out of a "< N" or
// "== N" comparison in a SuccessCriteria.Target prose string ("p99 < 250ms",
// "cart_error_ratio == 0"). Only "<" and "==" are authored today
// (authored.go) — anything else is refused, not guessed at.
var comparisonRe = regexp.MustCompile(`(<|==)\s*([\d.]+)`)

// parseTarget turns target's prose into a comparator ("<"/"==", or "=="
// for the bare "HEALTH_OK" special case) and the threshold to compare a
// metric's live value against. An unrecognized shape errors rather than
// silently falling back to some default comparison — a Converger that
// guesses at an operator it can't parse is worse than one that refuses to
// answer.
func parseTarget(target string) (op string, threshold float64, err error) {
	if target == "HEALTH_OK" {
		return "==", 0, nil
	}
	m := comparisonRe.FindStringSubmatch(target)
	if m == nil {
		return "", 0, fmt.Errorf("converge: cannot parse target %q", target)
	}
	f, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return "", 0, fmt.Errorf("converge: cannot parse target %q: %w", target, err)
	}
	return m[1], f, nil
}
