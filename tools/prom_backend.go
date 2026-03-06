package tools

import (
	"context"
	"fmt"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
)

// promBackend abstracts the differences between datasource types that support
// PromQL-compatible queries (native Prometheus, Cloud Monitoring, etc.).
type promBackend interface {
	// Query executes a PromQL query (instant or range) and returns the result.
	Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, error)

	// LabelNames returns label names, optionally filtered by matchers and time range.
	LabelNames(ctx context.Context, matchers []string, start, end time.Time) ([]string, error)

	// LabelValues returns values for a label, optionally filtered by matchers and time range.
	LabelValues(ctx context.Context, labelName string, matchers []string, start, end time.Time) ([]string, error)

	// MetricMetadata returns metadata about metrics (description, type, unit).
	MetricMetadata(ctx context.Context, metric string, limit int) (map[string][]promv1.Metadata, error)
}

// backendForDatasource looks up the datasource type and returns the appropriate backend.
func backendForDatasource(ctx context.Context, uid string) (promBackend, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	switch ds.Type {
	case "stackdriver":
		return newCloudMonitoringBackend(ctx, ds)
	default:
		// For prometheus, thanos, cortex, mimir, and any other Prometheus-compatible datasource,
		// use the native Prometheus client via the datasource proxy.
		return newPrometheusBackend(ctx, uid)
	}
}

// prometheusBackend wraps the Prometheus client library, talking to the
// datasource via Grafana's datasource proxy (/api/datasources/uid/{uid}/resources).
type prometheusBackend struct {
	api promv1.API
}

func newPrometheusBackend(ctx context.Context, uid string) (*prometheusBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/uid/%s/resources", trimTrailingSlash(cfg.URL), uid)

	rt, err := mcpgrafana.BuildTransport(&cfg, api.DefaultRoundTripper)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	if cfg.AccessToken != "" && cfg.IDToken != "" {
		rt = config.NewHeadersRoundTripper(&config.Headers{
			Headers: map[string]config.Header{
				"X-Access-Token": {
					Secrets: []config.Secret{config.Secret(cfg.AccessToken)},
				},
				"X-Grafana-Id": {
					Secrets: []config.Secret{config.Secret(cfg.IDToken)},
				},
			},
		}, rt)
	} else if cfg.APIKey != "" {
		rt = config.NewAuthorizationCredentialsRoundTripper(
			"Bearer", config.NewInlineSecret(cfg.APIKey), rt,
		)
	} else if cfg.BasicAuth != nil {
		password, _ := cfg.BasicAuth.Password()
		rt = config.NewBasicAuthRoundTripper(config.NewInlineSecret(cfg.BasicAuth.Username()), config.NewInlineSecret(password), rt)
	}

	rt = mcpgrafana.NewOrgIDRoundTripper(rt, cfg.OrgID)

	c, err := api.NewClient(api.Config{
		Address:      url,
		RoundTripper: rt,
	})
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client: %w", err)
	}

	return &prometheusBackend{api: promv1.NewAPI(c)}, nil
}

func (b *prometheusBackend) Query(ctx context.Context, expr string, queryType string, start, end time.Time, stepSeconds int) (model.Value, error) {
	switch queryType {
	case "range":
		step := time.Duration(stepSeconds) * time.Second
		result, _, err := b.api.QueryRange(ctx, expr, promv1.Range{
			Start: start,
			End:   end,
			Step:  step,
		})
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus range: %w", err)
		}
		return result, nil
	case "instant":
		result, _, err := b.api.Query(ctx, expr, start)
		if err != nil {
			return nil, fmt.Errorf("querying Prometheus instant: %w", err)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("invalid query type: %s", queryType)
	}
}

func (b *prometheusBackend) LabelNames(ctx context.Context, matchers []string, start, end time.Time) ([]string, error) {
	names, _, err := b.api.LabelNames(ctx, matchers, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label names: %w", err)
	}
	return names, nil
}

func (b *prometheusBackend) LabelValues(ctx context.Context, labelName string, matchers []string, start, end time.Time) ([]string, error) {
	values, _, err := b.api.LabelValues(ctx, labelName, matchers, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus label values: %w", err)
	}
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result, nil
}

func (b *prometheusBackend) MetricMetadata(ctx context.Context, metric string, limit int) (map[string][]promv1.Metadata, error) {
	metadata, err := b.api.Metadata(ctx, metric, fmt.Sprintf("%d", limit))
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus metric metadata: %w", err)
	}
	return metadata, nil
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
