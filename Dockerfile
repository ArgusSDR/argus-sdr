# Multi-stage build for Argus SDR
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev sqlite-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o argus-sdr .

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates sqlite curl docker-cli

# Create app user
RUN addgroup -g 1001 appgroup && \
    adduser -u 1001 -G appgroup -s /bin/sh -D appuser

# Create necessary directories
RUN mkdir -p /data /downloads /nice_data && \
    chown -R appuser:appgroup /data /downloads /nice_data

# Copy binary from builder
COPY --from=builder /app/argus-sdr /usr/local/bin/argus-sdr
RUN chmod +x /usr/local/bin/argus-sdr

# Switch to app user
USER appuser

# Set working directory
WORKDIR /app

# Expose default port
EXPOSE 8080

# Default command (can be overridden)
CMD ["argus-sdr", "api"]