package observability

import (
	"go.opentelemetry.io/otel/metric"
)

// Histogram bucket boundaries recommended by the OTel MCP semantic conventions.
// https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/
var mcpHistogramBuckets = metric.WithExplicitBucketBoundaries(
	0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 120, 300,
)
