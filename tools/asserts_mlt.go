package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

// entityLabelMapping defines how KG entity properties map to telemetry labels.
var entityLabelMapping = map[string]struct {
	PromLabels func(name, env, site, ns string) string
	LokiLabels func(name, env, site, ns string) string
	TraceAttrs func(name, env, site, ns string) string
}{
	"Service": {
		PromLabels: func(name, _, _, ns string) string {
			parts := []string{fmt.Sprintf(`job="%s"`, name)}
			if ns != "" {
				parts = append(parts, fmt.Sprintf(`namespace="%s"`, ns))
			}
			return "{" + strings.Join(parts, ", ") + "}"
		},
		LokiLabels: func(name, _, _, ns string) string {
			parts := []string{fmt.Sprintf(`app="%s"`, name)}
			if ns != "" {
				parts = append(parts, fmt.Sprintf(`namespace="%s"`, ns))
			}
			return "{" + strings.Join(parts, ", ") + "}"
		},
		TraceAttrs: func(name, _, _, ns string) string {
			parts := []string{fmt.Sprintf(`resource.service.name="%s"`, name)}
			if ns != "" {
				parts = append(parts, fmt.Sprintf(`resource.k8s.namespace.name="%s"`, ns))
			}
			return "{" + strings.Join(parts, " && ") + "}"
		},
	},
	"Node": {
		PromLabels: func(name, _, _, _ string) string {
			return fmt.Sprintf(`{instance=~"%s.*"}`, name)
		},
		LokiLabels: func(name, _, _, _ string) string {
			return fmt.Sprintf(`{node_name="%s"}`, name)
		},
		TraceAttrs: func(name, _, _, _ string) string {
			return fmt.Sprintf(`{resource.k8s.node.name="%s"}`, name)
		},
	},
	"Pod": {
		PromLabels: func(name, _, _, ns string) string {
			parts := []string{fmt.Sprintf(`pod="%s"`, name)}
			if ns != "" {
				parts = append(parts, fmt.Sprintf(`namespace="%s"`, ns))
			}
			return "{" + strings.Join(parts, ", ") + "}"
		},
		LokiLabels: func(name, _, _, ns string) string {
			parts := []string{fmt.Sprintf(`pod="%s"`, name)}
			if ns != "" {
				parts = append(parts, fmt.Sprintf(`namespace="%s"`, ns))
			}
			return "{" + strings.Join(parts, ", ") + "}"
		},
		TraceAttrs: func(name, _, _, ns string) string {
			parts := []string{fmt.Sprintf(`resource.k8s.pod.name="%s"`, name)}
			if ns != "" {
				parts = append(parts, fmt.Sprintf(`resource.k8s.namespace.name="%s"`, ns))
			}
			return "{" + strings.Join(parts, " && ") + "}"
		},
	},
	"Namespace": {
		PromLabels: func(name, _, _, _ string) string {
			return fmt.Sprintf(`{namespace="%s"}`, name)
		},
		LokiLabels: func(name, _, _, _ string) string {
			return fmt.Sprintf(`{namespace="%s"}`, name)
		},
		TraceAttrs: func(name, _, _, _ string) string {
			return fmt.Sprintf(`{resource.k8s.namespace.name="%s"}`, name)
		},
	},
}

// defaultMetrics returns key PromQL templates for an entity type.
// The %s placeholder is replaced with label matchers.
var defaultMetrics = map[string][]string{
	"Service": {
		`rate(http_server_requests_seconds_count%s[5m])`,
		`sum(rate(http_server_requests_seconds_count{status=~"5.."%s}[5m]))`,
		`histogram_quantile(0.99, sum(rate(http_server_requests_seconds_bucket%s[5m])) by (le))`,
	},
	"Node": {
		`100 - (avg by(instance) (rate(node_cpu_seconds_total{mode="idle"%s}[5m])) * 100)`,
		`node_memory_MemAvailable_bytes%s / node_memory_MemTotal_bytes%s * 100`,
	},
	"Pod": {
		`rate(container_cpu_usage_seconds_total%s[5m])`,
		`container_memory_working_set_bytes%s`,
	},
}

func buildPromLabels(entityType, name, env, site, ns string) string {
	if mapping, ok := entityLabelMapping[entityType]; ok {
		return mapping.PromLabels(name, env, site, ns)
	}
	parts := []string{fmt.Sprintf(`job="%s"`, name)}
	if ns != "" {
		parts = append(parts, fmt.Sprintf(`namespace="%s"`, ns))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func buildLokiLabels(entityType, name, env, site, ns string) string {
	if mapping, ok := entityLabelMapping[entityType]; ok {
		return mapping.LokiLabels(name, env, site, ns)
	}
	parts := []string{fmt.Sprintf(`app="%s"`, name)}
	if ns != "" {
		parts = append(parts, fmt.Sprintf(`namespace="%s"`, ns))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func buildTraceAttrs(entityType, name, env, site, ns string) string {
	if mapping, ok := entityLabelMapping[entityType]; ok {
		return mapping.TraceAttrs(name, env, site, ns)
	}
	return fmt.Sprintf(`{resource.service.name="%s"}`, name)
}

// injectLabels replaces %s in a PromQL template with label matchers.
// For templates like `metric{existing="filter"%s}`, the labels should be
// comma-prefixed. For `metric%s`, the full `{labels}` block is used.
func injectLabels(template, labels string) string {
	inner := strings.TrimPrefix(strings.TrimSuffix(labels, "}"), "{")
	count := strings.Count(template, "%s")
	args := make([]any, count)
	for i := range args {
		if strings.Contains(template, "{") {
			args[i] = ", " + inner
		} else {
			args[i] = labels
		}
	}
	return fmt.Sprintf(template, args...)
}

// --- get_entity_metrics ---

type GetEntityMetricsParams struct {
	EntityType    string    `json:"entityType" jsonschema:"required,description=Entity type (e.g. Service\\, Node\\, Pod)"`
	EntityName    string    `json:"entityName" jsonschema:"required,description=Entity name"`
	Env           string    `json:"env,omitempty" jsonschema:"description=Environment"`
	Site          string    `json:"site,omitempty" jsonschema:"description=Site"`
	Namespace     string    `json:"namespace,omitempty" jsonschema:"description=Namespace"`
	DatasourceUID string    `json:"datasourceUid" jsonschema:"required,description=Prometheus datasource UID to query"`
	MetricName    string    `json:"metricName,omitempty" jsonschema:"description=Specific PromQL expression. If omitted\\, returns default metrics for the entity type (request rate\\, error rate\\, latency for Service; CPU\\, memory for Node/Pod)."`
	StartTime     time.Time `json:"startTime" jsonschema:"required,description=Start time in RFC3339 format"`
	EndTime       time.Time `json:"endTime" jsonschema:"required,description=End time in RFC3339 format"`
	StepSeconds   int       `json:"stepSeconds,omitempty" jsonschema:"description=Step interval in seconds for range queries (default 60)"`
}

func getEntityMetrics(ctx context.Context, args GetEntityMetricsParams) (string, error) {
	assertsClient, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	entity, err := assertsClient.resolveEntityInfo(ctx, args.EntityType, args.EntityName, args.Env, args.Site, args.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve entity: %w", err)
	}

	labels := buildPromLabels(args.EntityType, entity.Name, entity.Env, entity.Site, entity.Namespace)

	step := args.StepSeconds
	if step <= 0 {
		step = 60
	}

	var queries []string
	if args.MetricName != "" {
		query := args.MetricName
		if !strings.Contains(query, "{") {
			query = query + labels
		}
		queries = []string{query}
	} else if templates, ok := defaultMetrics[args.EntityType]; ok {
		for _, tmpl := range templates {
			queries = append(queries, injectLabels(tmpl, labels))
		}
	} else {
		queries = []string{fmt.Sprintf(`up%s`, labels)}
	}

	type queryResult struct {
		Query  string `json:"query"`
		Result any    `json:"result,omitempty"`
		Error  string `json:"error,omitempty"`
	}

	results := make([]queryResult, 0, len(queries))
	for _, q := range queries {
		promArgs := QueryPrometheusParams{
			DatasourceUID: args.DatasourceUID,
			Expr:          q,
			StartTime:     args.StartTime.Format(time.RFC3339),
			EndTime:       args.EndTime.Format(time.RFC3339),
			StepSeconds:   step,
			QueryType:     "range",
		}
		result, queryErr := queryPrometheus(ctx, promArgs)
		qr := queryResult{Query: q}
		if queryErr != nil {
			qr.Error = queryErr.Error()
		} else {
			qr.Result = result
		}
		results = append(results, qr)
	}

	output := map[string]any{
		"entity":  entity.toSlim(),
		"labels":  labels,
		"metrics": results,
	}

	resultJSON, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metrics: %w", err)
	}
	return string(resultJSON), nil
}

var GetEntityMetrics = mcpgrafana.MustTool(
	"get_entity_metrics",
	"Get Prometheus metrics for a Knowledge Graph entity. Resolves entity labels from the KG and queries Prometheus. If no metric is specified, returns default metrics for the entity type (request rate, error rate, latency for Service; CPU, memory for Node/Pod).",
	getEntityMetrics,
	mcp.WithTitleAnnotation("Get KG entity metrics"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- get_entity_logs ---

type GetEntityLogsParams struct {
	EntityType    string    `json:"entityType" jsonschema:"required,description=Entity type (e.g. Service\\, Node\\, Pod)"`
	EntityName    string    `json:"entityName" jsonschema:"required,description=Entity name"`
	Env           string    `json:"env,omitempty" jsonschema:"description=Environment"`
	Site          string    `json:"site,omitempty" jsonschema:"description=Site"`
	Namespace     string    `json:"namespace,omitempty" jsonschema:"description=Namespace"`
	DatasourceUID string    `json:"datasourceUid" jsonschema:"required,description=Loki datasource UID to query"`
	Filter        string    `json:"filter,omitempty" jsonschema:"description=Log line filter expression (e.g. error\\, timeout\\, exception)"`
	StartTime     time.Time `json:"startTime" jsonschema:"required,description=Start time in RFC3339 format"`
	EndTime       time.Time `json:"endTime" jsonschema:"required,description=End time in RFC3339 format"`
	Limit         int       `json:"limit,omitempty" jsonschema:"description=Max log lines to return (default 50)"`
}

func getEntityLogs(ctx context.Context, args GetEntityLogsParams) (string, error) {
	assertsClient, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	entity, err := assertsClient.resolveEntityInfo(ctx, args.EntityType, args.EntityName, args.Env, args.Site, args.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve entity: %w", err)
	}

	streamSelector := buildLokiLabels(args.EntityType, entity.Name, entity.Env, entity.Site, entity.Namespace)

	query := streamSelector
	if args.Filter != "" {
		query = fmt.Sprintf(`%s |~ "%s"`, streamSelector, args.Filter)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	lokiClient, err := newLokiClient(ctx, args.DatasourceUID)
	if err != nil {
		return "", fmt.Errorf("failed to create Loki client: %w", err)
	}

	lokiResult, err := lokiClient.fetchQuery(ctx, fetchQueryParams{
		Query:     query,
		Start:     args.StartTime.Format(time.RFC3339),
		End:       args.EndTime.Format(time.RFC3339),
		Limit:     limit,
		Direction: "backward",
		QueryType: "range",
	})
	if err != nil {
		return "", fmt.Errorf("failed to query logs: %w", err)
	}

	output := map[string]any{
		"entity":  entity.toSlim(),
		"query":   query,
		"results": lokiResult,
	}

	outputJSON, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("failed to marshal output: %w", err)
	}
	return string(outputJSON), nil
}

var GetEntityLogs = mcpgrafana.MustTool(
	"get_entity_logs",
	"Get Loki logs for a Knowledge Graph entity. Resolves entity labels from the KG and queries Loki. Optionally filter log lines by a text pattern.",
	getEntityLogs,
	mcp.WithTitleAnnotation("Get KG entity logs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- get_entity_traces ---

type GetEntityTracesParams struct {
	EntityType    string    `json:"entityType" jsonschema:"required,description=Entity type (e.g. Service\\, Node\\, Pod)"`
	EntityName    string    `json:"entityName" jsonschema:"required,description=Entity name"`
	Env           string    `json:"env,omitempty" jsonschema:"description=Environment"`
	Site          string    `json:"site,omitempty" jsonschema:"description=Site"`
	Namespace     string    `json:"namespace,omitempty" jsonschema:"description=Namespace"`
	DatasourceUID string    `json:"datasourceUid" jsonschema:"required,description=Tempo datasource UID to query"`
	MinDuration   string    `json:"minDuration,omitempty" jsonschema:"description=Minimum trace duration filter (e.g. 500ms\\, 1s)"`
	StartTime     time.Time `json:"startTime" jsonschema:"required,description=Start time in RFC3339 format"`
	EndTime       time.Time `json:"endTime" jsonschema:"required,description=End time in RFC3339 format"`
	Limit         int       `json:"limit,omitempty" jsonschema:"description=Max traces to return (default 20)"`
}

func getEntityTraces(ctx context.Context, args GetEntityTracesParams) (string, error) {
	assertsClient, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	entity, err := assertsClient.resolveEntityInfo(ctx, args.EntityType, args.EntityName, args.Env, args.Site, args.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve entity: %w", err)
	}

	traceSelector := buildTraceAttrs(args.EntityType, entity.Name, entity.Env, entity.Site, entity.Namespace)
	query := traceSelector
	if args.MinDuration != "" {
		query = fmt.Sprintf(`%s | duration > %s`, traceSelector, args.MinDuration)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	tempoBaseURL := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(cfg.URL, "/"), args.DatasourceUID)

	transport, buildErr := mcpgrafana.BuildTransport(&cfg, nil)
	if buildErr != nil {
		return "", fmt.Errorf("failed to build transport: %w", buildErr)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	tempoClient := &Client{
		httpClient: &http.Client{Transport: mcpgrafana.NewUserAgentTransport(transport)},
		baseURL:    tempoBaseURL,
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("start", fmt.Sprintf("%d", args.StartTime.Unix()))
	params.Set("end", fmt.Sprintf("%d", args.EndTime.Unix()))

	data, err := tempoClient.fetchAssertsDataGet(ctx, "/tempo/api/search", params)
	if err != nil {
		output := map[string]any{
			"entity":  entity.toSlim(),
			"traceQL": query,
			"note":    "Tempo search failed. Use the traceQL query with query_loki_logs or a Tempo datasource tool.",
			"error":   err.Error(),
		}
		result, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			return "", fmt.Errorf("failed to marshal trace query: %w", marshalErr)
		}
		return string(result), nil
	}

	output := map[string]any{
		"entity":  entity.toSlim(),
		"query":   query,
		"results": json.RawMessage(data),
	}

	resultJSON, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("failed to marshal traces: %w", err)
	}
	return string(resultJSON), nil
}

var GetEntityTraces = mcpgrafana.MustTool(
	"get_entity_traces",
	"Get traces for a Knowledge Graph entity from Tempo. Resolves entity labels from the KG and constructs a TraceQL query. Optionally filter by minimum duration.",
	getEntityTraces,
	mcp.WithTitleAnnotation("Get KG entity traces"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
