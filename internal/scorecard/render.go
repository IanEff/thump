package scorecard

import (
	"fmt"
	"io"
	"sort"
)

// Render writes a Report as the human-readable table `calipers scorecard`
// prints by default. Section order — headline rate, then by-scenario, then
// by-run-index, then the failure-reason histogram, then every miss's
// RunID — walks from "is the number good" to "which run do I go read" in
// the order an operator actually asks those questions.
func Render(w io.Writer, rpt Report) {
	_, _ = fmt.Fprintf(w, "runs=%d hits=%d rate=%s harnessExcluded=%d\n", rpt.N, rpt.Hits, rate(rpt.Hits, rpt.N), rpt.HarnessExcluded)

	_, _ = fmt.Fprintln(w, "\nby scenario:")
	for _, name := range sortedKeys(rpt.ByScenario) {
		t := rpt.ByScenario[name]
		_, _ = fmt.Fprintf(w, "  %-30s hits=%d n=%d rate=%s\n", name, t.Hits, t.N, rate(t.Hits, t.N))
	}

	_, _ = fmt.Fprintln(w, "\nby run index:")
	for _, idx := range sortedIntKeys(rpt.ByRunIndex) {
		t := rpt.ByRunIndex[idx]
		_, _ = fmt.Fprintf(w, "  %-4d hits=%d n=%d rate=%s\n", idx, t.Hits, t.N, rate(t.Hits, t.N))
	}

	_, _ = fmt.Fprintln(w, "\nfailure reasons:")
	if len(rpt.Reasons) == 0 {
		_, _ = fmt.Fprintln(w, "  none")
	}
	for _, rs := range sortedReasonKeys(rpt.Reasons) {
		_, _ = fmt.Fprintf(w, "  %-24s %d\n", rs, rpt.Reasons[rs])
	}

	_, _ = fmt.Fprintln(w, "\nmisses:")
	if len(rpt.Misses) == 0 {
		_, _ = fmt.Fprintln(w, "  none")
	}
	for _, m := range rpt.Misses {
		_, _ = fmt.Fprintf(w, "  %-30s run=%-2d runID=%-20s reason=%s\n", m.ScenarioName, m.RunIndex, orDash(m.RunID), m.Reason)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func rate(hits, n int) string {
	if n == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(hits)/float64(n))
}

func sortedKeys(m map[string]Tally) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedIntKeys(m map[int]Tally) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedReasonKeys(m map[reason]int) []reason {
	keys := make([]reason, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
