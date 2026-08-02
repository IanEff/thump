package configfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/configfile"
)

// fixtureFile is a small staging struct standing in for a real one
// (weightsFile, limitsFile, walConfigFile, queryConfigFile) — every field a
// pointer, one of each kind Required has an accessor for.
type fixtureFile struct {
	Count  *int     `json:"count"`
	Bytes  *int64   `json:"bytes"`
	Ratio  *float64 `json:"ratio"`
	Window *string  `json:"window"`
}

var errIncompleteFixture = errors.New("fixture file is missing one or more required terms")

func TestStage_ReadsAndUnmarshalsAValidFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fixture.yaml")
	if err := os.WriteFile(path, []byte("count: 3\nbytes: 64\nratio: 0.5\nwindow: 5m\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := configfile.Stage[fixtureFile](path, "fixture file")
	if err != nil {
		t.Fatal(err)
	}

	want := 3
	if diff := cmp.Diff(&want, got.Count); diff != "" {
		t.Error("Stage did not unmarshal Count correctly (-want +got)\n", diff)
	}
}

func TestStage_WrapsTheReadErrorForAMissingFile(t *testing.T) {
	t.Parallel()
	_, err := configfile.Stage[fixtureFile](filepath.Join(t.TempDir(), "missing.yaml"), "fixture file")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want an error wrapping os.ErrNotExist, got %v", err)
	}
}

func TestStage_WrapsTheParseErrorForMalformedYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fixture.yaml")
	if err := os.WriteFile(path, []byte("count: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := configfile.Stage[fixtureFile](path, "fixture file")
	if err == nil {
		t.Fatal("want a parse error for malformed YAML, got nil")
	}
}

func TestRequiredAccessors_ReturnTheDereferencedValueWhenPresent(t *testing.T) {
	t.Parallel()
	r := configfile.Require("fixture file", errIncompleteFixture)

	count, bytes, ratio, window := 3, int64(64), 0.5, "5m"
	gotCount := r.Int("count", &count)
	gotBytes := r.Int64("bytes", &bytes)
	gotRatio := r.Float("ratio", &ratio)
	gotWindow := r.Duration("window", &window)

	if err := r.Err(); err != nil {
		t.Fatalf("want no error for a complete, well-formed fixture, got %v", err)
	}
	if diff := cmp.Diff(3, gotCount); diff != "" {
		t.Error("Int did not return the dereferenced value (-want +got)\n", diff)
	}
	if diff := cmp.Diff(int64(64), gotBytes); diff != "" {
		t.Error("Int64 did not return the dereferenced value (-want +got)\n", diff)
	}
	if diff := cmp.Diff(0.5, gotRatio); diff != "" {
		t.Error("Float did not return the dereferenced value (-want +got)\n", diff)
	}
	if diff := cmp.Diff(5*time.Minute, gotWindow); diff != "" {
		t.Error("Duration did not parse and return the staged string (-want +got)\n", diff)
	}
}

func TestRequiredAccessors_RecordANilPointerAsMissingAndReturnZero(t *testing.T) {
	t.Parallel()
	r := configfile.Require("fixture file", errIncompleteFixture)

	if got := r.Int("count", nil); got != 0 {
		t.Errorf("want 0 for a missing int, got %d", got)
	}
	if got := r.Int64("bytes", nil); got != 0 {
		t.Errorf("want 0 for a missing int64, got %d", got)
	}
	if got := r.Float("ratio", nil); got != 0 {
		t.Errorf("want 0 for a missing float64, got %f", got)
	}
	if got := r.Duration("window", nil); got != 0 {
		t.Errorf("want 0 for a missing duration, got %s", got)
	}

	err := r.Err()
	if !errors.Is(err, errIncompleteFixture) {
		t.Fatalf("want an error wrapping errIncompleteFixture, got %v", err)
	}
	want := "fixture file is missing one or more required terms: count, bytes, ratio, window"
	if diff := cmp.Diff(want, err.Error()); diff != "" {
		t.Error("missing keys must be reported in the order the accessors were called (-want +got)\n", diff)
	}
}

func TestRequired_KeepsOnlyTheFirstFailingDuration(t *testing.T) {
	t.Parallel()
	r := configfile.Require("fixture file", errIncompleteFixture)

	first, second := "not-a-duration", "also-not-a-duration"
	r.Duration("firstWindow", &first)
	r.Duration("secondWindow", &second)

	err := r.Err()
	if err == nil {
		t.Fatal("want a parse error for two malformed durations, got nil")
	}
	if !strings.Contains(err.Error(), "firstWindow") {
		t.Errorf("want the first failing duration's key (firstWindow) in the error, got %v", err)
	}
	if strings.Contains(err.Error(), "secondWindow") {
		t.Errorf("want only the first failing duration reported, got %v", err)
	}
}

func TestRequired_AMissingKeyOutranksAParseFailure(t *testing.T) {
	t.Parallel()
	r := configfile.Require("fixture file", errIncompleteFixture)

	bad := "not-a-duration"
	r.Int("count", nil)
	r.Duration("window", &bad)

	err := r.Err()
	if !errors.Is(err, errIncompleteFixture) {
		t.Errorf("want the missing-key sentinel to win over a parse failure, got %v", err)
	}
}

func TestRequired_ErrIsNilForACompleteWellFormedFixture(t *testing.T) {
	t.Parallel()
	r := configfile.Require("fixture file", errIncompleteFixture)

	count := 3
	r.Int("count", &count)

	if err := r.Err(); err != nil {
		t.Errorf("want nil for a complete, well-formed fixture, got %v", err)
	}
}
