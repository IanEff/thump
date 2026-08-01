package beat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/sealbox"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// NewS3Client builds an S3-compatible client (MinIO, s3mock, or real S3)
// from plain config values — so a beat's Main never has to import the AWS
// SDK itself to get one, the same hiding Tracer does for the OTel exporter.
// Every S3 consumer in this repo (the WAL shipper's segment sink, clank's
// transcript S3Store) must build its client through here, never
// s3.NewFromConfig directly — the GCS signing workarounds below are
// load-bearing for any of them, not just the shipper.
func NewS3Client(ctx context.Context, endpoint, accessKey, secretKey string) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("beat: load s3 config: %w", err)
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
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
	}), nil
}

// NewS3SegmentSink builds a publish.SegmentSink over an S3-compatible
// endpoint from plain config values, via NewS3Client above, wrapped in an
// EncryptingSink so every segment is sealed before it reaches the bucket.
func NewS3SegmentSink(ctx context.Context, endpoint, bucket, accessKey, secretKey string, key sealbox.Key) (publish.SegmentSink, error) {
	client, err := NewS3Client(ctx, endpoint, accessKey, secretKey)
	if err != nil {
		return nil, err
	}
	return &EncryptingSink{Inner: publish.NewS3SegmentSink(client, bucket), Key: key}, nil
}

// EncryptingSink seals a segment before handing it to Inner. The emptyDir
// copy on the pod stays plaintext on purpose — it dies with the pod, and
// it's what trim and the debug-transcript skill read during an incident. The
// copy that outlives the cluster is the one this decorator seals.
//
// Put reads the whole segment into memory to seal it, bounded by the WAL's
// 64 MiB seal threshold — if that cap is ever raised, this is the line that
// turns into an OOM.
type EncryptingSink struct {
	Inner publish.SegmentSink
	Key   sealbox.Key
}

func (s *EncryptingSink) Put(ctx context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("encrypting sink: read %s: %w", key, err)
	}
	sealed, err := s.Key.Seal(b)
	if err != nil {
		return fmt.Errorf("encrypting sink: seal %s: %w", key, err)
	}
	return s.Inner.Put(ctx, key, bytes.NewReader(sealed))
}

// UnsealSegment reverses EncryptingSink.Put for one bucket object, returning
// the segment's JSON lines. Nothing in the running system calls it — a beat
// only ever writes — but a segment nobody can read back is an audit trail in
// name only, and the alternative is re-deriving the seal envelope by hand
// during an incident.
func UnsealSegment(key sealbox.Key, r io.Reader) ([][]byte, error) {
	sealed, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("unseal segment: read: %w", err)
	}
	plain, err := key.Open(sealed)
	if err != nil {
		// The usual cause is a key that survived one base64 decode too few:
		// kubectl hands back the Secret's stored value already encoded on top
		// of THUMP_SEAL_KEY's own encoding, and a wrong key of the right
		// length fails authentication exactly like a corrupt segment does.
		return nil, fmt.Errorf("unseal segment: open (wrong seal key, or not a sealed segment): %w", err)
	}

	var lines [][]byte
	for line := range bytes.SplitSeq(bytes.TrimSuffix(plain, []byte("\n")), []byte("\n")) {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// RunShipper ships wal's sealed segments to sink on shipInterval until ctx
// is cancelled — the async half of the Mimir pattern: WALPublisher.Publish
// already returned once the segment was durable on local disk, so a slow
// or failing ship never sits in the hot path. Meant to run under an
// errgroup alongside a beat's consumer loop, same shape as
// clank/broker.go's two-subscriber composition.
func RunShipper(ctx context.Context, wal *publish.WAL, sink publish.SegmentSink, shipInterval time.Duration) {
	PollLoop(ctx, PollConfig{Interval: shipInterval, Timeout: 4 * shipInterval}, func(ctx context.Context) error {
		return wal.Ship(ctx, sink)
	})
}
