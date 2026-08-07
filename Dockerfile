FROM --platform=$BUILDPLATFORM golang:1.26 AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM deps AS build
ARG BEAT
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
COPY . .
# otelc regenerates .otelc-build/, the per-command otelc.runtime.go files and the
# go.mod replace directives on each invocation, then reverts them when the build
# finishes. Nothing it produces is committed, so there is nothing to set up first.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go tool otelc go build -ldflags "-s -w \
    -X main.version=${VERSION} \
    -X main.commit=${COMMIT} \
    -X main.date=${DATE}" -o /out/${BEAT} ./cmd/${BEAT}

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/ianeff/thump"
ARG BEAT
COPY --from=build /out/${BEAT} /usr/local/bin/beat
ENTRYPOINT ["/usr/local/bin/beat"]
