package tools

import (
	"context"
	"io"
	"net/http"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"go.opentelemetry.io/otel/metric"
)

func prometheusDiscoveryMeter(ctx context.Context) metric.Meter {
	return mcpgrafana.GrafanaConfigFromContext(ctx).MeterProviderOrDefault().Meter("mcp-grafana")
}

func recordPrometheusMetricNames(ctx context.Context, count int) {
	histogram, _ := prometheusDiscoveryMeter(ctx).Int64Histogram("mcp.prometheus.metric_names.count",
		metric.WithDescription("Number of metric names in a successful upstream response before local pagination"),
		metric.WithUnit("{name}"),
	)
	histogram.Record(ctx, int64(count))
}

type prometheusMetricNamesTransport struct {
	underlying http.RoundTripper
}

func (t *prometheusMetricNamesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.underlying.RoundTrip(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || !strings.HasSuffix(req.URL.Path, "/api/v1/label/__name__/values") {
		return resp, err
	}
	histogram, _ := prometheusDiscoveryMeter(req.Context()).Int64Histogram("mcp.prometheus.metric_names.response.size",
		metric.WithDescription("Bytes read from a complete successful metric-name HTTP response before JSON decoding and pagination"),
		metric.WithUnit("By"),
	)
	resp.Body = &metricNamesResponseBody{ReadCloser: resp.Body, ctx: req.Context(), histogram: histogram}
	return resp, nil
}

type metricNamesResponseBody struct {
	io.ReadCloser
	ctx       context.Context
	histogram metric.Int64Histogram
	bytes     int64
	recorded  bool
}

func (b *metricNamesResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes += int64(n)
	// Count the bytes actually consumed, including decompressed/chunked bodies, without buffering a second copy.
	if err == io.EOF && !b.recorded {
		b.histogram.Record(b.ctx, b.bytes)
		b.recorded = true
	}
	return n, err
}
