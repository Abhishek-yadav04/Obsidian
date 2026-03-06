# Build stage
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set build arguments
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application (supports multi-arch via TARGETARCH)
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s -X main.AppVersion=${VERSION} -X main.GitCommit=${COMMIT}" \
    -o /obsidian ./cmd/obsidian

# Final stage
FROM alpine:3.23

# Re-declare build ARG for use in this stage
ARG VERSION=dev

# OCI Image Labels (enterprise metadata)
LABEL org.opencontainers.image.title="Obsidian WAF" \
      org.opencontainers.image.description="Enterprise Web Application Firewall based on Coraza engine" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/corazawaf/obsidian" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.vendor="Project OBSIDIAN"

# Install runtime dependencies and create non-root user
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 obsidian && \
    adduser -u 1000 -G obsidian -s /bin/sh -D obsidian

WORKDIR /app

# Copy binary from builder with explicit ownership
COPY --from=builder --chown=obsidian:obsidian /obsidian /app/obsidian

# Copy configuration files with explicit ownership
COPY --chown=obsidian:obsidian coraza.conf-recommended /app/coraza.conf
COPY --chown=obsidian:obsidian default.conf /app/default.conf

# Create directories for data
RUN mkdir -p /app/data /app/logs && \
    chown -R obsidian:obsidian /app

# Switch to non-root user
USER obsidian

# Expose ports
EXPOSE 8082

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8082/api/health || exit 1

# Set environment variables
ENV OBSIDIAN_PORT=8082 \
    OBSIDIAN_LOG_LEVEL=info \
    OBSIDIAN_LOG_FORMAT=json

# Graceful shutdown signal
STOPSIGNAL SIGTERM

# Run the application
ENTRYPOINT ["/app/obsidian"]
CMD ["-port", "8082"]
