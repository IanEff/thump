# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install dependencies first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build both binaries
# Use CGO_ENABLED=0 to ensure static binaries
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /bin/clank ./cmd/clank
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /bin/rattle ./cmd/rattle

# --- Clank Image ---
FROM alpine:latest AS clank

RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /bin/clank /app/clank

# Assuming clank might need to expose a port, e.g., 8080
EXPOSE 8080

ENTRYPOINT ["/app/clank"]

# --- Rattle Image ---
FROM alpine:latest AS rattle

RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /bin/rattle /app/rattle

# Assuming rattle might need to expose a port, e.g., 8081
EXPOSE 8081

ENTRYPOINT ["/app/rattle"]
