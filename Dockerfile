# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.26.4-alpine AS builder

WORKDIR /app

# Download dependencies first (layer-cache friendly)
COPY go.mod go.sum ./
RUN go mod download

# Build the static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o postgres-mcp \
      .

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM alpine:3.21

# CA certificates are needed for TLS connections to cloud-hosted PostgreSQL.
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/postgres-mcp .

# Port used when running in SSE transport mode
EXPOSE 8000

# DATABASE_URI and ACCESS_MODE can be supplied as environment variables
# (preferred for Docker). The positional argument is also accepted.
ENV ACCESS_MODE=unrestricted

ENTRYPOINT ["/app/postgres-mcp"]
