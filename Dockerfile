# Build stage
FROM golang:1.24-alpine3.23 AS builder

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN go build -o mcp-grafana ./cmd/mcp-grafana

# Final stage
FROM alpine:3.23.3

LABEL io.modelcontextprotocol.server.name="io.github.grafana/mcp-grafana"

# Install ca-certificates for HTTPS requests and upgrade existing packages
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates

# Create a non-root user
RUN adduser -D -u 1000 -h /home/mcp-grafana mcp-grafana

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
