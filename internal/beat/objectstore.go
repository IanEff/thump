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
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
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
		// GCS's S3-compatibility XML API doesn't understand the SDK's default
		// chunked/trailer-checksum PutObject wire format (STREAMING-...-TRAILER) —
		// it computes a different signature than the SDK sent, so every write 403s
		// with SignatureDoesNotMatch. WhenRequired restores plain single-shot SigV4
		// signing, which GCS does understand.
		//
		// The chunked-checksum default arrived in service/s3 v1.73.0 (the SDK's
		// "default integrity protections" change); this package pins v1.105.0
		// (go.mod), so it's always in range and this override is load-bearing, not
		// defensive. Pre-v1.73.0 the override is a no-op — safe either way.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		// The SDK also always signs Accept-Encoding (it explicitly sets it to
		// "identity" to keep control of gzip handling — see
		// service/internal/accept-encoding's DisableGzip middleware). Google's
		// front-end proxy rewrites that header in transit to
		// "identity,gzip(gfe)" before it reaches the bucket's signature
		// verifier, so the signature GCS recomputes never matches what the SDK
		// sent — a second, independent SignatureDoesNotMatch, distinct from the
		// checksum one above and not fixed by it. aws-cli/botocore never signs
		// Accept-Encoding at all, which is why a CLI probe against the same
		// bucket succeeds while this client 403s without this. Stripping the
		// header just before signing removes it from SignedHeaders entirely,
		// so GCS's rewrite is a no-op for verification; net/http still adds
		// its own unsigned Accept-Encoding: gzip at the transport layer and
		// transparently decompresses, unaffected by this.
		o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
			return stack.Finalize.Insert(
				middleware.FinalizeMiddlewareFunc("StripAcceptEncodingBeforeSigning",
					func(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (
						middleware.FinalizeOutput, middleware.Metadata, error,
					) {
						if req, ok := in.Request.(*smithyhttp.Request); ok {
							req.Header.Del("Accept-Encoding")
						}
						return next.HandleFinalize(ctx, in)
					}),
				"Signing", middleware.Before,
			)
		})
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
