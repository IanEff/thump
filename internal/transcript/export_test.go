package transcript

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/sealbox"
)

// FindSetForTest exposes findSet to transcript_test — the join logic itself,
// independent of Main's flag/env wiring.
func FindSetForTest(ctx context.Context, client *s3.Client, key sealbox.Key, bucket, runID string) (proposal.Set, bool, error) {
	return findSet(ctx, client, key, bucket, runID)
}
