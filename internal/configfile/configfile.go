// Package configfile enforces two-stage YAML loading across thump — missing
// keys fail at load time rather than silently defaulting to zero values at
// runtime. It stages YAML into pointer structs to distinguish omitted keys
// from explicit zeros, then collects all missing required fields into a single
// error.
package configfile

import (
	"fmt"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// Stage reads path and unmarshals it into F, a staging struct whose every
// field is a pointer so an omitted key stays distinguishable from a zero
// value. noun names the file in both returned errors.
func Stage[F any](path, noun string) (F, error) {
	var zero F
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config path, not user input
	if err != nil {
		return zero, fmt.Errorf("read %s: %w", noun, err)
	}
	var staged F
	if err := yaml.Unmarshal(raw, &staged); err != nil {
		return zero, fmt.Errorf("parse %s: %w", noun, err)
	}
	return staged, nil
}

// Required collects every field a staged file left unset, so one load
// reports every omitted key at once instead of the first. Duration fields
// stage as *string — sigs.k8s.io/yaml round-trips through encoding/json,
// which cannot parse "30m" into a time.Duration directly — so Required also
// carries the first parse failure among them, checked only once every
// required key is confirmed present.
type Required struct {
	noun     string
	sentinel error
	missing  []string
	parseErr error
}

// Require starts a Required collector — sentinel is the error every miss is
// wrapped in, noun names the file an accessor's parse error references.
func Require(noun string, sentinel error) *Required {
	return &Required{noun: noun, sentinel: sentinel}
}

// Int reads a required int field. A nil p is an omitted key: key is
// recorded as missing and Int returns 0.
func (r *Required) Int(key string, p *int) int {
	if p == nil {
		r.missing = append(r.missing, key)
		return 0
	}
	return *p
}

// Int64 is Int for a field staged as *int64.
func (r *Required) Int64(key string, p *int64) int64 {
	if p == nil {
		r.missing = append(r.missing, key)
		return 0
	}
	return *p
}

// Float is Int for a field staged as *float64.
func (r *Required) Float(key string, p *float64) float64 {
	if p == nil {
		r.missing = append(r.missing, key)
		return 0
	}
	return *p
}

// Duration reads a required field staged as *string and reparses it with
// time.ParseDuration. A nil p is recorded as missing, the same as the other
// accessors; a present but unparseable string is not, since the key was not
// omitted — its first such failure is kept in place of every later one.
func (r *Required) Duration(key string, p *string) time.Duration {
	if p == nil {
		r.missing = append(r.missing, key)
		return 0
	}
	d, err := time.ParseDuration(*p)
	if err != nil {
		if r.parseErr == nil {
			r.parseErr = fmt.Errorf("%s %s: %w", r.noun, key, err)
		}
		return 0
	}
	return d
}

// Err reports the sentinel wrapping every missing key, then the first
// duration parse failure, then nil — a missing key always outranks a parse
// failure, since the loaders this collector serves fail at load with one
// fault surfaced, never a value silently zeroed.
func (r *Required) Err() error {
	if len(r.missing) > 0 {
		return fmt.Errorf("%w: %s", r.sentinel, strings.Join(r.missing, ", "))
	}
	return r.parseErr
}
