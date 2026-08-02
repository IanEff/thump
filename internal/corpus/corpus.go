// Package corpus mines the sealed WAL into internal/clank's calibration
// record.
package corpus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/sealbox"
)

// Getter is the object-store read this package needs beyond listing -
// satisfied by *s3.client.
type Getter interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// Walk lists every object under prefix in a bucket, unseals each with key,
// and returns the segment keys walked alongside every decoded line
// across them, in listing order.
func Walk(ctx context.Context, lister s3.ListObjectsV2APIClient, getter Getter, key sealbox.Key, bucket, prefix string) ([]string, [][]byte, error) {
	var segmentKeys []string
	var lines [][]byte

	p := s3.NewListObjectsV2Paginator(lister, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("corpus: list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			segLines, err := getSegment(ctx, getter, key, bucket, *obj.Key)
			if err != nil {
				return nil, nil, err
			}
			segmentKeys = append(segmentKeys, *obj.Key)
			lines = append(lines, segLines...)
		}
	}
	return segmentKeys, lines, nil
}

func getSegment(ctx context.Context, getter Getter, key sealbox.Key, bucket, objKey string) ([][]byte, error) {
	out, err := getter.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(objKey)})
	if err != nil {
		return nil, fmt.Errorf("corpus: get %s: %w", objKey, err)
	}
	defer func() { _ = out.Body.Close() }()

	lines, err := objectstore.UnsealSegment(key, out.Body)
	if err != nil {
		return nil, fmt.Errorf("corpus: unseal %s: %w", objKey, err)
	}
	return lines, nil
}

// DecodeEach unmarshals every line into a fresh T, stopping at the first line
// that doesn't parse.
func DecodeEach[T any](lines [][]byte) ([]T, error) {
	out := make([]T, 0, len(lines))
	for _, line := range lines {
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("corpus: decode: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}
