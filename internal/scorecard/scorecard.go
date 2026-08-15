package scorecard

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// Main reads --results (or stdin, the pipe `task harvest:dev | tee out.jsonl
// | calipers scorecard` leaves open) as newline-delimited harvest.Result and
// prints a Report. Returns 0 once the report printed, whatever rate it
// carries — a low rate is the finding this phase exists to surface, not a
// process failure. Returns 1 only if reading or decoding itself failed.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scorecard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resultsPath := fs.String("results", "-", "path to a harvest --json output file, or - for stdin")
	asJSON := fs.Bool("json", false, "print the Report as JSON instead of a human table")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	r := io.Reader(os.Stdin)
	if *resultsPath != "-" {
		f, err := os.Open(*resultsPath) //nolint:gosec // G304: operator-supplied path, the command's whole purpose
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "scorecard:", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		r = f
	}

	rpt, err := Grade(r)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "scorecard:", err)
		return 1
	}

	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(rpt); err != nil {
			_, _ = fmt.Fprintln(stderr, "scorecard:", err)
			return 1
		}
		return 0
	}

	Render(stdout, rpt)
	return 0
}
