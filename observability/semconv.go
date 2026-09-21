package observability

import (
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Histogram bucket boundaries recommended by the OTel MCP semantic conventions.
// https://opentelemetry.io/docs/specs/semconv/gen-ai/mcp/
var mcpHistogramBuckets = metric.WithExplicitBucketBoundaries(
	0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 30, 60, 120, 300,
)

// PrometheusDiscoveryHistogramView enables native histograms for metric-name discovery.
// Embedders should install this view on the provider supplied in GrafanaConfig.
func PrometheusDiscoveryHistogramView() sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: "mcp.prometheus.metric_names.*", Kind: sdkmetric.InstrumentKindHistogram},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: 160, MaxScale: 8}},
	)
}
