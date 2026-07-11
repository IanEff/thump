package beat

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/publish"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ShipInterval is the fixed cadence RunShipper checks a WAL for sealed,
// unshipped segments. Not (yet) a configured knob — every beat ships on
// the same schedule.
const ShipInterval = 30 * time.Second

// NewS3SegmentSink builds a publish.SegmentSink over an S3-compatible
// endpoint (MinIO, s3mock, or real S3) from plain config values — so a
// beat's Main never has to import the AWS SDK itself to get one, the same
// hiding Tracer does for the OTel exporter.
func NewS3SegmentSink(ctx context.Context, endpoint, bucket, accessKey, secretKey string) (publish.SegmentSink, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("beat: load s3 config: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	return publish.NewS3SegmentSink(client, bucket), nil
}

// RunShipper ships wal's sealed segments to sink on ShipInterval until ctx
// is cancelled — the async half of the Mimir pattern: WALPublisher.Publish
// already returned once the segment was durable on local disk, so a slow
// or failing ship never sits in the hot path. Meant to run under an
// errgroup alongside a beat's consumer loop, same shape as
// clank/broker.go's two-subscriber composition.
func RunShipper(ctx context.Context, wal *publish.WAL, sink publish.SegmentSink) {
	PollLoop(ctx, PollConfig{Interval: ShipInterval}, func(ctx context.Context) error {
		return wal.Ship(ctx, sink)
	})
}
