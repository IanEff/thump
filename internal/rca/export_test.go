package rca

import (
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// WriteSetForTest exposes writeSet to rca_test.
func WriteSetForTest(transcripts, fixture string, set proposal.Set) error {
	return writeSet(transcripts, fixture, set)
}

// LoadDetectionForTest exposes loadDetection to rca_test.
func LoadDetectionForTest(name string) (signal.Detection, error) {
	return loadDetection(name)
}
