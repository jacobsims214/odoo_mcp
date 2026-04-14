# syntax=docker/dockerfile:1

# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Cache dependencies separately from source
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -o /odoo-mcp \
    ./cmd/odoo-mcp

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /odoo-mcp /odoo-mcp

# Default to stdio transport (override with ODOO_MCP_TRANSPORT=http for networked use)
ENV ODOO_MCP_TRANSPORT=stdio

ENTRYPOINT ["/odoo-mcp"]
