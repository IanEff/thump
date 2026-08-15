package corpus_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/corpus"
	"github.com/ianeff/thump/internal/sealbox"
)

// incidentCase builds a minimal clank.Case for exercising CollapseCases's
// group key: decisionRef is "dec:" + signalRef + ":" + an arbitrary id, so
// SignalRef is recovered from it the same way a pre-tag artifact's is; sec
// becomes ObservedAt as a Unix timestamp so cases can be ordered by it.
func incidentCase(decisionRef, outcomeRef string, result outcome.Result, sec int64) clank.Case {
	rest := strings.TrimPrefix(decisionRef, "dec:")
	signalRef := rest[:strings.LastIndex(rest, ":")]
	return clank.Case{
		SignalRef:   signalRef,
		DecisionRef: decisionRef,
		OutcomeRef:  outcomeRef,
		Result:      result,
		ObservedAt:  time.Unix(sec, 0),
	}
}

// fakeBucket is the in-memory ObjectStore double: a fixed key→sealed-bytes
// map, listed in one page — Walk's pagination loop is exercised by the SDK's
// own paginator, so the fake only needs to prove Walk decrypts and orders
// what it lists.
type fakeBucket struct {
	objects map[string][]byte // key -> sealed bytes
}

func (b *fakeBucket) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	var contents []types.Object
	for key := range b.objects {
		contents = append(contents, types.Object{Key: aws.String(key)})
	}
	return &s3.ListObjectsV2Output{Contents: contents, IsTruncated: aws.Bool(false)}, nil
}

func (b *fakeBucket) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	body, ok := b.objects[*in.Key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func TestWalk_UnsealsEveryObjectUnderThePrefix(t *testing.T) {
	t.Parallel()
	var key sealbox.Key // zero key: sealbox.Key{}.Open/Seal round-trip regardless of value
	sealed, err := key.Seal([]byte(`{"a":1}` + "\n" + `{"a":2}`))
	if err != nil {
		t.Fatal(err)
	}
	bucket := &fakeBucket{objects: map[string][]byte{"clank/thump.proposals/seg-0001.wal": sealed}}

	segmentKeys, lines, err := corpus.Walk(context.Background(), bucket, bucket, key, "test-bucket", "clank/thump.proposals/")
	if err != nil {
		t.Fatal(err)
	}

	wantKeys := []string{"clank/thump.proposals/seg-0001.wal"}
	if diff := cmp.Diff(wantKeys, segmentKeys); diff != "" {
		t.Error("wrong segment keys walked", diff)
	}
	wantLines := [][]byte{[]byte(`{"a":1}`), []byte(`{"a":2}`)}
	if diff := cmp.Diff(wantLines, lines); diff != "" {
		t.Error("wrong lines decoded from the segment", diff)
	}
}

// TestWalk_SkipsAnUnsealableSegmentAndStillWalksTheGoodOnes pins the fix for
// a rotated seal.key aborting the whole mine: one segment sealed under a key
// Walk can't open must not stop it from returning every other segment's
// lines, mirroring tune.Run's identical shape for a broken transcript.
func TestWalk_SkipsAnUnsealableSegmentAndStillWalksTheGoodOnes(t *testing.T) {
	t.Parallel()
	var key sealbox.Key
	sealed, err := key.Seal([]byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	bucket := &fakeBucket{objects: map[string][]byte{
		"clank/thump.proposals/seg-0001.wal": []byte("not a sealed segment"),
		"clank/thump.proposals/seg-0002.wal": sealed,
	}}

	segmentKeys, lines, err := corpus.Walk(context.Background(), bucket, bucket, key, "test-bucket", "clank/thump.proposals/")
	if err != nil {
		t.Fatal(err)
	}

	wantKeys := []string{"clank/thump.proposals/seg-0002.wal"}
	if diff := cmp.Diff(wantKeys, segmentKeys); diff != "" {
		t.Error("wrong segment keys walked past the unreadable one", diff)
	}
	wantLines := [][]byte{[]byte(`{"a":1}`)}
	if diff := cmp.Diff(wantLines, lines); diff != "" {
		t.Error("wrong lines decoded from the readable segment", diff)
	}
}

// TestWalk_ErrorsWhenEverySegmentIsUnsealable pins the other side: silently
// reporting an empty corpus when nothing under the prefix could be read is
// worse than erroring, so Walk still fails loud in that case.
func TestWalk_ErrorsWhenEverySegmentIsUnsealable(t *testing.T) {
	t.Parallel()
	var key sealbox.Key
	bucket := &fakeBucket{objects: map[string][]byte{
		"clank/thump.proposals/seg-0001.wal": []byte("not a sealed segment"),
	}}

	if _, _, err := corpus.Walk(context.Background(), bucket, bucket, key, "test-bucket", "clank/thump.proposals/"); err == nil {
		t.Error("want an error when every segment under the prefix fails to unseal, got nil")
	}
}

func TestReadCorpus_RecoversTheFingerprintAPreTagArtifactLost(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		file string
		want string
	}{
		"readCorpus reads Fingerprint when the untagged artifact still carries it": {
			file: "testdata/corpus-legacy-fingerprint.json",
			want: "slo_burn:ceph-cluster",
		},
		"readCorpus rebuilds the fingerprint from DecisionRef when the rename emptied it": {
			file: "testdata/corpus-legacy-emptied.json",
			want: "slo_burn:ceph-cluster",
		},
		"readCorpus reads signalRef directly when the artifact carries a version": {
			file: "testdata/corpus-v2.json",
			want: "slo_burn:ceph-cluster",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := corpus.ReadCorpus(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got.Cases[0].SignalRef); diff != "" {
				t.Error("migration lost the join key", diff)
			}
		})
	}
}

func TestReadCorpus_LeavesRunIDEmptyOnAnArtifactThatPredatesTheField(t *testing.T) {
	t.Parallel()
	// v2 never had RunID at all — decoding it must leave the field at its
	// zero value, not fabricate one the way migrateLegacy recovers SignalRef
	// for the pre-tag layout.
	got, err := corpus.ReadCorpus("testdata/corpus-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("", got.Cases[0].RunID); diff != "" {
		t.Error("a pre-RunID artifact should decode with RunID empty, not guessed", diff)
	}
}

func TestReadCorpus_RefusesAnArtifactNewerThanThisBuildWrites(t *testing.T) {
	t.Parallel()
	// A best-effort decode of an unknown layout is what produced six cases
	// with an empty fingerprint — a zero value that reads as real data. Fail
	// at load, where a human is looking.
	_, err := corpus.ReadCorpus("testdata/corpus-v99.json")
	if !errors.Is(err, corpus.ErrUnknownCorpusVersion) {
		t.Error("want ErrUnknownCorpusVersion", err)
	}
}

func TestCollapseCases_KeepsOneCasePerIncidentAcrossAMerge(t *testing.T) {
	t.Parallel()
	// terminalOutcome runs over freshly-mined records only, so an artifact
	// written before that landed keeps its per-record rows forever, and the
	// first number a tuner reads is 3x the number of incidents behind it.
	// applied is an execute-time status superseded by whatever the
	// convergence watcher settles; the settled record is the case.
	cases := map[string]struct {
		in   []clank.Case
		want []clank.Case
	}{
		"CollapseCases keeps the settled case when an incident also recorded applied": {
			in: []clank.Case{
				incidentCase("dec:slo_burn:ceph-cluster:1", "out:a", outcome.ResultApplied, 100),
				incidentCase("dec:slo_burn:ceph-cluster:1", "out:b", outcome.ResultPartialNonConverging, 200),
				incidentCase("dec:slo_burn:ceph-cluster:1", "out:c", outcome.ResultApplied, 300),
			},
			want: []clank.Case{
				incidentCase("dec:slo_burn:ceph-cluster:1", "out:b", outcome.ResultPartialNonConverging, 200),
			},
		},
		"CollapseCases keeps both when two incidents share a fingerprint": {
			// A re-detection is a second incident, not a duplicate of the
			// first — the DecisionRef is what separates them.
			in: []clank.Case{
				incidentCase("dec:slo_burn:ceph-cluster:1", "out:a", outcome.ResultSuccess, 100),
				incidentCase("dec:slo_burn:ceph-cluster:2", "out:b", outcome.ResultSuccess, 200),
			},
			want: []clank.Case{
				incidentCase("dec:slo_burn:ceph-cluster:1", "out:a", outcome.ResultSuccess, 100),
				incidentCase("dec:slo_burn:ceph-cluster:2", "out:b", outcome.ResultSuccess, 200),
			},
		},
		"CollapseCases drops an incident that only ever recorded applied": {
			// Nothing settled it, so there is no calibration datum here yet —
			// counting it would be counting a run still in flight.
			in: []clank.Case{
				incidentCase("dec:slo_burn:ceph-cluster:1", "out:a", outcome.ResultApplied, 100),
			},
			want: nil,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, clank.CollapseCases(tc.in)); diff != "" {
				t.Error("wrong cases survived the collapse", diff)
			}
		})
	}
}
