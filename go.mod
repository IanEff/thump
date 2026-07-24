module github.com/ianeff/thump

go 1.26.5

require (
	charm.land/lipgloss/v2 v2.0.3
	github.com/anthropics/anthropic-sdk-go v1.53.0
	github.com/aws/aws-sdk-go-v2 v1.42.1
	github.com/aws/aws-sdk-go-v2/config v1.32.29
	github.com/aws/aws-sdk-go-v2/credentials v1.19.28
	github.com/aws/aws-sdk-go-v2/service/s3 v1.105.0
	github.com/aws/smithy-go v1.27.3
	github.com/google/go-cmp v0.7.0
	github.com/invopop/jsonschema v0.14.0
	github.com/johannesboyne/gofakes3 v1.2.0
	github.com/nats-io/nats-server/v2 v2.14.3
	github.com/nats-io/nats.go v1.52.0
	github.com/prometheus/client_golang v1.23.2
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0
	go.opentelemetry.io/otel/sdk v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
	go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/init v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/sdk/trace v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/trace v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/google.golang.org/grpc/client v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/google.golang.org/grpc/server v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/log v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/log/slog v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/net/http/client v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/net/http/server v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otelc/instrumentation/runtime v0.0.0-00010101000000-000000000000
	go.yaml.in/yaml/v2 v2.4.4
	golang.org/x/sync v0.21.0
	google.golang.org/genai v1.62.0
	k8s.io/api v0.36.2
	k8s.io/apimachinery v0.36.2
	k8s.io/client-go v0.36.2
	sigs.k8s.io/yaml v1.6.0
)

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/dave/dst v0.27.4 // indirect
	github.com/gofrs/flock v0.13.0 // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/urfave/cli/v3 v3.10.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	go.opentelemetry.io/contrib/bridges/prometheus v0.69.0 // indirect
	go.opentelemetry.io/contrib/exporters/autoexport v0.69.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/runtime v0.69.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.20.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.20.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/prometheus v0.66.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdoutlog v0.20.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.44.0 // indirect
	go.opentelemetry.io/otel/log v0.20.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.20.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.44.0 // indirect
	go.opentelemetry.io/otelc/instrumentation v0.0.0-00010101000000-000000000000 // indirect
	go.opentelemetry.io/otelc/pkg v0.0.0 // indirect
	go.opentelemetry.io/otelc/pkg/runtime v0.0.0-00010101000000-000000000000 // indirect
	golang.org/x/mod v0.37.0 // indirect
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.9.3 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/antithesishq/antithesis-sdk-go v0.7.0-default-no-op // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.14 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.30 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.4.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.32.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.37.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.44.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20251205161215-1948445e3318 // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/moby/spdystream v0.5.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/jwt/v2 v2.8.2 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.67.5 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/ryszard/goskiplist v0.0.0-20150312221310-2dfbae5fcf46 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otelc v1.0.1 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.shabbyrobe.org/gocovmerge v0.0.0-20230507111327-fa4f82cfbf4d // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/term v0.44.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.82.0 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260317180543-43fb72c5454a // indirect
	k8s.io/streaming v0.36.2 // indirect
	k8s.io/utils v0.0.0-20260210185600-b8788abfbbc2 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.2 // indirect
)

tool go.opentelemetry.io/otelc/tool/cmd/otelc

replace go.opentelemetry.io/otelc/instrumentation/runtime => /Users/ian/projects/go/thump/.otelc-build/instrumentation/runtime

replace go.opentelemetry.io/otelc/instrumentation/net/http/server => /Users/ian/projects/go/thump/.otelc-build/instrumentation/net/http/server

replace go.opentelemetry.io/otelc/instrumentation/net/http/client => /Users/ian/projects/go/thump/.otelc-build/instrumentation/net/http/client

replace go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/trace => /Users/ian/projects/go/thump/.otelc-build/instrumentation/go.opentelemetry.io/otel/trace

replace go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel => /Users/ian/projects/go/thump/.otelc-build/instrumentation/go.opentelemetry.io/otel

replace go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/init => /Users/ian/projects/go/thump/.otelc-build/instrumentation/go.opentelemetry.io/otel/init

replace go.opentelemetry.io/otelc/instrumentation/log => /Users/ian/projects/go/thump/.otelc-build/instrumentation/log

replace go.opentelemetry.io/otelc/instrumentation/log/slog => /Users/ian/projects/go/thump/.otelc-build/instrumentation/log/slog

replace go.opentelemetry.io/otelc/instrumentation/google.golang.org/grpc/client => /Users/ian/projects/go/thump/.otelc-build/instrumentation/google.golang.org/grpc/client

replace go.opentelemetry.io/otelc/instrumentation/google.golang.org/grpc/server => /Users/ian/projects/go/thump/.otelc-build/instrumentation/google.golang.org/grpc/server

replace go.opentelemetry.io/otelc/instrumentation/go.opentelemetry.io/otel/sdk/trace => /Users/ian/projects/go/thump/.otelc-build/instrumentation/go.opentelemetry.io/otel/sdk/trace

replace go.opentelemetry.io/otelc/pkg => /Users/ian/projects/go/thump/.otelc-build/pkg

replace go.opentelemetry.io/otelc/pkg/runtime => /Users/ian/projects/go/thump/.otelc-build/pkg/runtime

replace go.opentelemetry.io/otelc/instrumentation => /Users/ian/projects/go/thump/.otelc-build/instrumentation
