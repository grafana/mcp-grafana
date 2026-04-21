package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/prometheus/common/model"
)

const victoriaMetricsDatasourceType = "victoriametrics-metrics-datasource"

// victoriaMetricsBackend implements promBackend for the VictoriaMetrics
// Grafana plugin (type: victoriametrics-metrics-datasource).
//
// Discovery endpoints (label names, label values, metric metadata) are served
// correctly by the plugin's resource proxy and reuse the native Prometheus
// backend. Only Query is overridden to go through Grafana's /api/ds/query
// endpoint, because the plugin's resource proxy does not expose
// /api/v1/query — hitting it returns the plugin's "Hello from VM data source!"
// landing response and trips the Prometheus client's JSON parser.
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
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
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
//
// The VictoriaMetrics plugin distinguishes instant vs range via two boolean
// fields on the query payload (instant/range), unlike Prometheus where the
// distinction is the URL path.
func (b *victoriaMetricsBackend) Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, error) {
	if start.IsZero() {
		start = end
	}
	if end.IsZero() || end.Before(start) {
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

	resp, err := b.doDSQuery(ctx, payload)
	if err != nil {
		if queryType == "range" {
			return nil, fmt.Errorf("querying VictoriaMetrics range: %w", err)
		}
		return nil, fmt.Errorf("querying VictoriaMetrics instant: %w", err)
	}

	return framesToPrometheusValue(resp, queryType)
}

// doDSQuery executes a request against Grafana's /api/ds/query endpoint.
// Mirrors the Cloud Monitoring backend's helper of the same name; kept local
// to avoid coupling the two backends.
func (b *victoriaMetricsBackend) doDSQuery(ctx context.Context, payload map[string]interface{}) (*dsQueryResponse, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/api/ds/query", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query returned status %d: %s", resp.StatusCode, string(body[:min(len(body), 1024)]))
	}

	var queryResp dsQueryResponse
	if err := json.Unmarshal(body, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	return &queryResp, nil
}
