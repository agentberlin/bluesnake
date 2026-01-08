# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /build

# Install build dependencies for CGO (required by SQLite)
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the server binary with CGO enabled (required for SQLite)
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o bluesnake-server ./cmd/testserver

# Runtime stage - use a minimal image with Chrome for JS rendering
FROM debian:bookworm-slim

# Install runtime dependencies
# - ca-certificates: for HTTPS requests
# - chromium: for JS rendering (headless browser)
# - fonts: for proper text rendering in screenshots
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    fonts-liberation \
    fonts-noto-color-emoji \
    && rm -rf /var/lib/apt/lists/*

# Set Chrome path for chromedp
ENV CHROME_PATH=/usr/bin/chromium

# Create non-root user for security
# Set home directory to /data so ~/.bluesnake database is persisted in the volume
RUN useradd -r -u 1001 -g root -d /data bluesnake

# Create data directory for SQLite database
RUN mkdir -p /data && chown bluesnake:root /data

# Set HOME explicitly so the app stores data in the mounted volume
ENV HOME=/data

WORKDIR /app

# Copy only the binary from builder
COPY --from=builder /build/bluesnake-server .

# Set ownership
RUN chown bluesnake:root /app/bluesnake-server

USER bluesnake

# Expose the default port
EXPOSE 8080

# Data volume for persistent storage
VOLUME ["/data"]

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/v1/health || exit 1

ENTRYPOINT ["./bluesnake-server"]
CMD ["--host", "0.0.0.0", "--port", "8080"]
