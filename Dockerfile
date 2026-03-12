# ══════════════════════════════════════════════════════════════
# NAT Punchthrough Hero — Multi-stage Docker Build
# ══════════════════════════════════════════════════════════════
#
# Stage 1: Build Go binary (~800MB, discarded)
# Stage 2: Final image with just the binary (~10MB)
#
# Build:
#   docker build -t natpunch-server .
#
# Build with version:
#   docker build --build-arg VERSION=1.2.3 -t natpunch-server .

# ── Build Stage ──────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

# Build args
ARG VERSION=dev

# Install certificates and timezone data (needed at runtime)
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /src

# Copy go module files first for layer caching
COPY server/go.mod server/go.sum ./
RUN go mod download

# Copy source code
COPY server/ .

# Build the binary
# CGO_ENABLED=0 for a fully static binary
# -trimpath removes file system paths from the binary
# -ldflags sets version and strips debug info
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /natpunch-server \
    .

# ── Runtime Stage ────────────────────────────────────────────
FROM scratch

# Import certificates and timezone data from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the binary
COPY --from=builder /natpunch-server /natpunch-server

# Copy default config if present (optional)
COPY config.example.toml /app/config.example.toml

# Expose default ports
# 8080 = REST API + WebSocket
# 80   = HTTP (ACME challenge)
# 443  = HTTPS
EXPOSE 8080 80 443

# Health check (uses the built-in binary)
# Note: scratch has no shell; use the binary's health check mode
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD ["/natpunch-server", "health"]

# Run the server
ENTRYPOINT ["/natpunch-server"]
CMD ["serve"]
