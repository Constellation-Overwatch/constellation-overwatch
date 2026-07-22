# Build Stage
FROM golang:1.25.12-alpine AS builder

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
FROM alpine:3.21

WORKDIR /app

# Install minimal runtime dependencies
RUN apk add --no-cache ca-certificates tzdata && \
    mkdir -p /data

# Copy binary from builder
COPY --from=builder /app/bin/overwatch /app/overwatch

# Set default data directory (DB at /data/db/, NATS at /data/overwatch/)
ENV OVERWATCH_DATA_DIR=/data

# Expose ports
EXPOSE 4222 8080

ENTRYPOINT ["/app/overwatch", "start"]
