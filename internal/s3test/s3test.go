// Package s3test spins up an in-process, in-memory S3-compatible server for
// tests (gofakes3 over httptest, no Docker, no network) and returns a ready
// client. It is imported only from _test.go files across the module — never
// from production code — so the fake server never links into a shipped
// binary, the same discipline internal/natstest applies to its embedded
// NATS server.
package s3test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

// bucket is the fixed bucket name every New call provisions — tests don't
// need to choose their own, they just need one that already exists.
const bucket = "test-transcripts"

// New starts an embedded fake S3 server, provisions bucket, and returns a
// client pointed at it plus the bucket name. The server is torn down via
// t.Cleanup.
func New(t *testing.T) (*s3.Client, string) {
	t.Helper()

	faker := gofakes3.New(s3mem.New())
	ts := httptest.NewServer(faker.Server())
	t.Cleanup(ts.Close)

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("dummy", "dummy", "")),
	)
	if err != nil {
		t.Fatal("load aws config:", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})

	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatal("create test bucket:", err)
	}

	return client, bucket
}
