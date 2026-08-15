package corpus

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/sealbox"
)

// WriteCorpusForTest exposes writeCorpus to corpus_test — the merge-then-write
// path, independent of Main's S3 wiring.
func WriteCorpusForTest(path string, mined clank.Corpus) error {
	return writeCorpus(path, mined)
}

// MineForTest exposes mine to corpus_test — the join-and-label path,
// independent of Main's config/env wiring.
func MineForTest(ctx context.Context, client *s3.Client, key sealbox.Key, bucket string) (clank.Corpus, populations, error) {
	return mine(ctx, client, key, bucket)
}

// PopulationsForTest constructs a populations value for corpus_test —
// unexported fields, external test package.
func PopulationsForTest(journaled, labelled, inFlight, unlabelled int) populations {
	return populations{Journaled: journaled, Labelled: labelled, InFlight: inFlight, Unlabelled: unlabelled}
}

// MergeCorpusForTest exposes mergeCorpus to corpus_test.
func MergeCorpusForTest(existing, mined clank.Corpus) clank.Corpus {
	return mergeCorpus(existing, mined)
}

// ReadCorpusForTest exposes readCorpus to corpus_test — the version branch
// and the legacy migration, independent of Main's S3 wiring.
func ReadCorpusForTest(path string) (clank.Corpus, error) {
	return readCorpus(path)
}
