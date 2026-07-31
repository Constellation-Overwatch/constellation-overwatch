# Build Stage
FROM golang:1.25.12-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application (pure Go, no CGO needed)
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/overwatch ./cmd/microlith

# Run Stage
FROM alpine:3.21.6@sha256:c3f8e73fdb79deaebaa2037150150191b9dcbfba68b4a46d70103204c53f4709

WORKDIR /app

# Install minimal runtime dependencies
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 10001 overwatch && \
    adduser -S -D -H -u 10001 -G overwatch overwatch && \
    mkdir -p /data && \
    chown -R 10001:10001 /data /app

# Copy binary from builder
COPY --from=builder /app/bin/overwatch /app/overwatch

# Set default data directory (DB at /data/db/, NATS at /data/overwatch/)
ENV OVERWATCH_DATA_DIR=/data \
    HOST=0.0.0.0 \
    PORT=8080 \
    NATS_HOST=127.0.0.1 \
    NATS_PORT=4224

VOLUME ["/data"]
EXPOSE 8080

USER 10001:10001

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -T 2 -O - http://127.0.0.1:8080/health >/dev/null || exit 1

ENTRYPOINT ["/app/overwatch", "start"]
