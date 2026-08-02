package corpus_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/corpus"
	"github.com/ianeff/thump/internal/sealbox"
)

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
