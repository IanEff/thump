package publish

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var _ SegmentSink = (*S3SegmentSink)(nil)

// S3SegmentSink is the SegmentSink WAL.Ship uploads sealed segments through
// in production — a thin wrapper over the AWS SDK's S3 client, the same
// shape JetPublisher already gives nats.go: the concrete transport lives
// beside the port it satisfies, not behind a separate package.
type S3SegmentSink struct {
	Client *s3.Client
	Bucket string
}

// NewS3SegmentSink returns an S3SegmentSink writing objects to bucket via
// client.
func NewS3SegmentSink(client *s3.Client, bucket string) *S3SegmentSink {
	return &S3SegmentSink{Client: client, Bucket: bucket}
}

// Put uploads r's contents to key under Bucket.
func (s *S3SegmentSink) Put(ctx context.Context, key string, r io.Reader) error {
	_, err := s.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
		Body:   r,
	})
	if err != nil {
		return fmt.Errorf("s3 segment sink: put %s: %w", key, err)
	}
	return nil
}
