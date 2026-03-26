# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS builder

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application using cross-compilation instead of QEMU emulation.
# TARGETOS and TARGETARCH are automatically set by Docker BuildKit.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o mcp-grafana ./cmd/mcp-grafana

# Final stage
FROM debian:bookworm-slim@sha256:f06537653ac770703bc45b4b113475bd402f451e85223f0f2837acbf89ab020a

LABEL io.modelcontextprotocol.server.name="io.github.grafana/mcp-grafana"

# Install ca-certificates for HTTPS requests and upgrade existing packages
# to pick up security fixes (e.g. OpenSSL) newer than the base image snapshot
RUN apt-get update && apt-get upgrade -y && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

# Create a non-root user
RUN useradd -r -u 1000 -m mcp-grafana

# Set the working directory
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder --chown=1000:1000 /app/mcp-grafana /app/

# Use the non-root user
USER mcp-grafana

# Expose the port the app runs on
EXPOSE 8000

# Run the application
ENTRYPOINT ["/app/mcp-grafana", "--transport", "sse", "--address", "0.0.0.0:8000"]
