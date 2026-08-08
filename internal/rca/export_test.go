package rca

import "github.com/ianeff/thump/api/v1/proposal"

// WriteSetForTest exposes writeSet to rca_test.
func WriteSetForTest(transcripts, fixture string, set proposal.Set) error {
	return writeSet(transcripts, fixture, set)
}
