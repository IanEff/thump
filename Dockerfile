FROM --platform=$BUILDPLATFORM golang:1.26 AS deps
WORKDIR /src
COPY go.mod go.sum otel.instrumentation.go ./
COPY .otelc-build/ .otelc-build/
RUN go mod download

FROM deps AS build
ARG BEAT
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
COPY . .
# Install otelc for compile-time instrumentation
RUN go install go.opentelemetry.io/otelc/tool/cmd/otelc@latest
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH otelc go build -ldflags "-s -w \
    -X main.version=${VERSION} \
    -X main.commit=${COMMIT} \
    -X main.date=${DATE}" -o /out/${BEAT} ./cmd/${BEAT}

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/ianeff/thump"
ARG BEAT
COPY --from=build /out/${BEAT} /usr/local/bin/beat
ENTRYPOINT ["/usr/local/bin/beat"]
