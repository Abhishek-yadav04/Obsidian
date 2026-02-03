# Build stage
FROM golang:1.23-alpine AS builder

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
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 obsidian && \
    adduser -u 1000 -G obsidian -s /bin/sh -D obsidian

WORKDIR /app

# Copy binary from builder
COPY --from=builder /obsidian /app/obsidian

# Copy configuration files
COPY coraza.conf-recommended /app/coraza.conf
COPY default.conf /app/default.conf

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

# Run the application
ENTRYPOINT ["/app/obsidian"]
CMD ["-port", "8082"]
