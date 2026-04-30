package tools

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/prometheus/common/model"
)

const victoriaMetricsDatasourceType = "victoriametrics-metrics-datasource"

// victoriaMetricsBackend implements promBackend for the VictoriaMetrics
// Grafana plugin. Discovery endpoints reuse the native Prometheus backend via
// the resource proxy; Query is routed through /api/ds/query because the
// plugin's resource proxy does not expose /api/v1/query.
type victoriaMetricsBackend struct {
	*prometheusBackend
	httpClient    *http.Client
	baseURL       string
	datasourceUID string
}

func newVictoriaMetricsBackend(ctx context.Context, uid string, ds *models.DataSource) (*victoriaMetricsBackend, error) {
	promBackend, err := newPrometheusBackend(ctx, uid, ds)
	if err != nil {
		return nil, err
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := trimTrailingSlash(cfg.URL)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	return &victoriaMetricsBackend{
		prometheusBackend: promBackend,
		httpClient:        client,
		baseURL:           baseURL,
		datasourceUID:     ds.UID,
	}, nil
}

// Query executes a PromQL/MetricsQL query via Grafana's /api/ds/query endpoint.
// The VM plugin signals instant vs range with two boolean fields on the payload.
func (b *victoriaMetricsBackend) Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, error) {
	if queryType != "instant" && queryType != "range" {
		return nil, fmt.Errorf("invalid query type: %s", queryType)
	}

	if start.IsZero() && end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		start = end
	}
	if end.Before(start) {
		end = start
	}

	step := stepSeconds
	if step == 0 {
		step = 60
	}

	query := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"type": victoriaMetricsDatasourceType,
			"uid":  b.datasourceUID,
		},
		"expr":       expr,
		"instant":    queryType == "instant",
		"range":      queryType == "range",
		"interval":   fmt.Sprintf("%ds", step),
		"intervalMs": int64(step) * 1000,
	}

	payload := map[string]interface{}{
		"queries": []interface{}{query},
		"from":    strconv.FormatInt(start.UnixMilli(), 10),
		"to":      strconv.FormatInt(end.UnixMilli(), 10),
	}

	resp, err := doDSQuery(ctx, b.httpClient, b.baseURL, payload)
	if err != nil {
		return nil, fmt.Errorf("querying VictoriaMetrics %s: %w", queryType, err)
	}

	return framesToPrometheusValue(resp, queryType)
}
