package objectstore_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestObjectstoreIsALeafPackage pins that internal/objectstore stays a
// leaf: stdlib, the AWS SDK plus its underlying smithy-go transport, and
// the publish/sealbox ports it seals segments through — never a beat.
// internal/corpus and internal/unseal, neither a beat, depend on this
// package directly.
func TestObjectstoreIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		leaftest.Stdlib,
		"github.com/ianeff/thump/internal/publish",
		"github.com/ianeff/thump/internal/sealbox",
		"github.com/aws/aws-sdk-go-v2/aws",
		"github.com/aws/aws-sdk-go-v2/config",
		"github.com/aws/aws-sdk-go-v2/credentials",
		"github.com/aws/aws-sdk-go-v2/service/s3",
		"github.com/aws/smithy-go/middleware",
		"github.com/aws/smithy-go/transport/http",
	)
}
