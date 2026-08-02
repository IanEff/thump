package unseal_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/unseal"
)

// TestKeyFromEnv pins the diagnosis of the mistake this tool exists to absorb.
// A key read out of kubectl without a second base64 -d decodes to the wrong
// length, and every later failure looks like a corrupt segment instead of a
// bad key.
func TestKeyFromEnv(t *testing.T) {
	t.Parallel()

	good := make([]byte, 32)
	good[0] = 7

	tests := map[string]struct {
		env     string
		wantErr bool
	}{
		"KeyFromEnv accepts a base64 value decoding to exactly 32 bytes": {
			env: base64.StdEncoding.EncodeToString(good),
		},
		"KeyFromEnv tolerates the trailing newline a shell pipeline leaves on": {
			env: base64.StdEncoding.EncodeToString(good) + "\n",
		},
		"KeyFromEnv rejects a value still carrying kubectl's own base64 layer": {
			env:     base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(good))),
			wantErr: true,
		},
		"KeyFromEnv rejects a value that is not base64 at all": {
			env:     "not base64 at all!",
			wantErr: true,
		},
		"KeyFromEnv rejects an unset key rather than opening with zeroes": {
			env:     "",
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := unseal.KeyFromEnv(tc.env)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error, got none")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(sealbox.Key(good), got); diff != "" {
				t.Error("wrong key decoded (-want +got)\n", diff)
			}
		})
	}
}

// TestSummarize_ShowsWhetherAnyCausalScoreLandedInTopology pins the one fact
// this reader was built to surface. Every score reading InTopology false is
// what a broken change-to-topology join looks like from the outside, and it is
// invisible in the emitted confidence number alone.
func TestSummarize_ShowsWhetherAnyCausalScoreLandedInTopology(t *testing.T) {
	t.Parallel()

	set := proposal.Set{
		SignalRef:    "slo_burn:cephblockpool/1785513492554845248",
		FailureClass: proposal.ClassRedundancyDegraded,
		Recommended:  "p1",
		Proposals: []proposal.Candidate{
			{ID: "p1", Rank: 1, ContractRef: "accelerate-recovery", Confidence: 0.72, Citations: []string{"a", "b"}},
		},
		CausalScores: []proposal.CausalScore{
			{EventID: "abc123", InTopology: true, Likelihood: 0.63},
			{EventID: "def456", Likelihood: 0.5},
		},
		Gate: &proposal.GateResult{Passed: true},
	}
	line, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}

	got := unseal.Summarize(line)
	for _, want := range []string{
		"slo_burn:cephblockpool",
		"accelerate-recovery",
		"confidence=0.720",
		"abc123 inTopology=true likelihood=0.630",
		"def456 inTopology=false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q, got:\n%s", want, got)
		}
	}
}

// TestSummarize_FallsBackToTheRawLineForAnotherBeatsObject keeps a
// wrong-subject pull legible: every beat's segments share this format, so a
// rattle segment opened here is a mistake worth showing rather than an error
// worth stopping on.
func TestSummarize_FallsBackToTheRawLineForAnotherBeatsObject(t *testing.T) {
	t.Parallel()

	line := []byte(`{"fingerprint":"fp-1","name":"burn_rate_acceleration"}`)
	if diff := cmp.Diff(string(line)+"\n", unseal.Summarize(line)); diff != "" {
		t.Error("a line that is not a ProposalSet must print verbatim (-want +got)\n", diff)
	}
}

// TestMain_ReadsASealedSegmentEndToEnd drives the binary the way an operator
// does: a segment sealed exactly as the shipper seals it, on disk, opened with
// the key from the environment.
func TestMain_ReadsASealedSegmentEndToEnd(t *testing.T) {
	var key sealbox.Key
	key[0] = 9

	set := proposal.Set{
		SignalRef:    "slo_burn:cephblockpool/1",
		Recommended:  "p1",
		Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "accelerate-recovery", Confidence: 0.72}},
		CausalScores: []proposal.CausalScore{{EventID: "abc123", InTopology: true, Likelihood: 0.63}},
	}
	line, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}

	// Sealed through EncryptingSink rather than sealbox directly, so this test
	// fails if the shipping side's envelope ever changes.
	inner := &captureSink{}
	sink := &objectstore.EncryptingSink{Inner: inner, Key: key}
	if err := sink.Put(context.Background(), "clank/thump.proposals/seg-1", bytes.NewReader(append(line, '\n'))); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "seg-1")
	if err := os.WriteFile(path, inner.body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THUMP_SEAL_KEY", base64.StdEncoding.EncodeToString(key[:]))

	var stdout, stderr bytes.Buffer
	if code := unseal.Main([]string{path}, &stdout, &stderr); code != 0 {
		t.Fatalf("unseal exited %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "abc123 inTopology=true") {
		t.Errorf("want the causal detail in stdout, got:\n%s", stdout.String())
	}
}

func TestMain_ExitsNonZeroForTheWrongKey(t *testing.T) {
	var key sealbox.Key
	key[0] = 9

	inner := &captureSink{}
	sink := &objectstore.EncryptingSink{Inner: inner, Key: key}
	if err := sink.Put(context.Background(), "clank/thump.proposals/seg-1", bytes.NewReader([]byte("{}\n"))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "seg-1")
	if err := os.WriteFile(path, inner.body, 0o600); err != nil {
		t.Fatal(err)
	}

	var other sealbox.Key
	other[0] = 1
	t.Setenv("THUMP_SEAL_KEY", base64.StdEncoding.EncodeToString(other[:]))

	var stdout, stderr bytes.Buffer
	if code := unseal.Main([]string{path}, &stdout, &stderr); code == 0 {
		t.Error("unseal exited 0 with the wrong seal key, so a failed open reads as an empty segment")
	}
}

// captureSink keeps the sealed bytes EncryptingSink hands its inner sink.
type captureSink struct{ body []byte }

func (c *captureSink) Put(_ context.Context, _ string, r io.Reader) error {
	b, err := io.ReadAll(r)
	c.body = b
	return err
}
