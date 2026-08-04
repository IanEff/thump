package corpus

import "github.com/ianeff/thump/internal/clank"

// WriteCorpusForTest exposes writeCorpus to corpus_test — the merge-then-write
// path, independent of Main's S3 wiring.
func WriteCorpusForTest(path string, mined clank.Corpus) error {
	return writeCorpus(path, mined)
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
