package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/grafana-openapi-client-go/models"
)

type GenerateDeeplinkParams struct {
	ResourceType  string            `json:"resourceType" jsonschema:"required,description=Type of resource: dashboard\\, panel\\, or explore"`
	DashboardUID  *string           `json:"dashboardUid,omitempty" jsonschema:"description=Dashboard UID (required for dashboard and panel types)"`
	DatasourceUID *string           `json:"datasourceUid,omitempty" jsonschema:"description=Datasource UID. Required for explore type. Optional for dashboard/panel types: when provided\\, auto-resolves the datasource template variable so callers don't need to know the exact var-datasource value."`
	PanelID       *int              `json:"panelId,omitempty" jsonschema:"description=Panel ID (required for panel type)"`
	Queries       []ExploreQuery    `json:"queries,omitempty" jsonschema:"description=Queries for explore links. For PromQL/LogQL datasources use expr. For Elasticsearch/Coralogix datasources use extraJSON with query\\, metrics\\, bucketAggs\\, and timeField."`
	QueryParams   map[string]string `json:"queryParams,omitempty" jsonschema:"description=Additional query parameters"`
	TimeRange     *TimeRange        `json:"timeRange,omitempty" jsonschema:"description=Time range for the link"`
}

type ExploreQuery struct {
	Expr     string                 `json:"expr,omitempty" jsonschema:"description=Query expression (PromQL\\, LogQL\\, etc.). Required for Prometheus/Loki datasources."`
	RefID    string                 `json:"refId,omitempty" jsonschema:"description=Reference ID for the query (defaults to A)"`
	Instant  *bool                  `json:"instant,omitempty" jsonschema:"description=Whether to run as instant query"`
	Range    *bool                  `json:"range,omitempty" jsonschema:"description=Whether to run as range query"`
	ExtraJSON map[string]interface{} `json:"extraJSON,omitempty" jsonschema:"description=Additional datasource-specific query properties (e.g. query\\, metrics\\, bucketAggs\\, timeField for Elasticsearch/Coralogix datasources). These are merged directly into the query object."`
}

type TimeRange struct {
	From string `json:"from" jsonschema:"description=Start time. Use relative (e.g. 'now-1h') or epoch milliseconds (e.g. '1773721800000'). ISO timestamps are not supported by Grafana."`
	To   string `json:"to" jsonschema:"description=End time. Use relative (e.g. 'now') or epoch milliseconds (e.g. '1773723000000'). ISO timestamps are not supported by Grafana."`
}

// buildExploreLeftParam constructs the JSON value for the `left` URL parameter
// used by Grafana Explore. The `left` param must contain the datasource, queries,
// and time range as a single JSON object.
func buildExploreLeftParam(datasourceUID string, queries []ExploreQuery, timeRange *TimeRange) (string, error) {
	// Build query objects
	queryList := make([]map[string]interface{}, 0)
	if len(queries) > 0 {
		for i, q := range queries {
			qm := map[string]interface{}{
				"refId":      q.RefID,
				"datasource": map[string]string{"uid": datasourceUID},
			}
			if qm["refId"] == "" {
				qm["refId"] = string(rune('A' + i))
			}

			// If ExtraJSON is provided, merge those properties directly into the
			// query object. This supports datasources like Elasticsearch/Coralogix
			// that use fields like "query", "metrics", "bucketAggs", "timeField"
			// instead of "expr".
			if len(q.ExtraJSON) > 0 {
				for k, v := range q.ExtraJSON {
					qm[k] = v
				}
			}

			// Only set expr/editorMode for PromQL/LogQL-style datasources
			// (i.e., when ExtraJSON is not used or expr is explicitly provided)
			if q.Expr != "" {
				qm["expr"] = q.Expr
				qm["editorMode"] = "code"
			}

			if q.Range != nil {
				qm["range"] = *q.Range
			} else if q.Expr != "" {
				// Default range=true only for PromQL/LogQL queries
				qm["range"] = true
			}
			if q.Instant != nil {
				qm["instant"] = *q.Instant
			}
			queryList = append(queryList, qm)
		}
	} else {
		// Default empty query
		queryList = append(queryList, map[string]interface{}{
			"refId":      "A",
			"datasource": map[string]string{"uid": datasourceUID},
		})
	}

	leftObj := map[string]interface{}{
		"datasource": datasourceUID,
		"queries":    queryList,
	}

	// Embed time range inside the left object
	if timeRange != nil {
		rangeObj := map[string]string{}
		if timeRange.From != "" {
			rangeObj["from"] = timeRange.From
		}
		if timeRange.To != "" {
			rangeObj["to"] = timeRange.To
		}
		if len(rangeObj) > 0 {
			leftObj["range"] = rangeObj
		}
	}

	leftJSON, err := json.Marshal(leftObj)
	if err != nil {
		return "", err
	}
	return string(leftJSON), nil
}

// resolveDatasourceVariable resolves the datasource template variable for a dashboard.
// When a datasourceUID is provided for a dashboard/panel link, this function fetches
// the dashboard JSON, finds the datasource-type template variable, looks up the
// datasource by UID, and injects the correct var-<name>=<value> into queryParams.
// This eliminates the need for callers to know the exact internal variable value
// that Grafana expects (which differs from both the display name and UID).
func resolveDatasourceVariable(ctx context.Context, dashboardUID string, datasourceUID string, queryParams map[string]string) (map[string]string, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	if c == nil {
		return queryParams, nil
	}

	// Fetch the dashboard JSON
	dashboard, err := c.Dashboards.GetDashboardByUID(dashboardUID)
	if err != nil {
		return queryParams, fmt.Errorf("failed to fetch dashboard %s for variable resolution: %w", dashboardUID, err)
	}

	// Find the datasource-type template variable name
	varName, err := findDatasourceVariableName(dashboard.Payload)
	if err != nil || varName == "" {
		return queryParams, nil // No datasource variable found — nothing to resolve
	}

	// Check if the caller already set this variable
	varKey := "var-" + varName
	if _, exists := queryParams[varKey]; exists {
		return queryParams, nil // Caller already set it explicitly — don't override
	}

	// Look up the datasource by UID to get its name
	ds, err := c.Datasources.GetDataSourceByUID(datasourceUID)
	if err != nil {
		return queryParams, fmt.Errorf("failed to look up datasource %s: %w", datasourceUID, err)
	}

	// Initialize queryParams if nil
	if queryParams == nil {
		queryParams = make(map[string]string)
	}
	queryParams[varKey] = ds.Payload.Name
	return queryParams, nil
}

// findDatasourceVariableName inspects a dashboard's templating.list to find the
// first datasource-type template variable and returns its name.
func findDatasourceVariableName(dashboard *models.DashboardFullWithMeta) (string, error) {
	if dashboard == nil || dashboard.Dashboard == nil {
		return "", nil
	}

	db, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return "", nil
	}

	templating, ok := db["templating"].(map[string]interface{})
	if !ok {
		return "", nil
	}

	list, ok := templating["list"].([]interface{})
	if !ok {
		return "", nil
	}

	for _, item := range list {
		variable, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		varType, _ := variable["type"].(string)
		if varType == "datasource" {
			name, _ := variable["name"].(string)
			if name != "" {
				return name, nil
			}
		}
	}

	return "", nil
}

func generateDeeplink(ctx context.Context, args GenerateDeeplinkParams) (string, error) {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(config.URL, "/")

	if baseURL == "" {
		return "", fmt.Errorf("grafana url not configured. Please set GRAFANA_URL environment variable or X-Grafana-URL header")
	}

	var deeplink string

	switch strings.ToLower(args.ResourceType) {
	case "dashboard":
		if args.DashboardUID == nil {
			return "", fmt.Errorf("dashboardUid is required for dashboard links")
		}
		// Auto-resolve datasource variable if datasourceUID is provided
		if args.DatasourceUID != nil {
			var err error
			args.QueryParams, err = resolveDatasourceVariable(ctx, *args.DashboardUID, *args.DatasourceUID, args.QueryParams)
			if err != nil {
				return "", err
			}
		}
		deeplink = fmt.Sprintf("%s/d/%s", baseURL, *args.DashboardUID)
	case "panel":
		if args.DashboardUID == nil {
			return "", fmt.Errorf("dashboardUid is required for panel links")
		}
		if args.PanelID == nil {
			return "", fmt.Errorf("panelId is required for panel links")
		}
		// Auto-resolve datasource variable if datasourceUID is provided
		if args.DatasourceUID != nil {
			var err error
			args.QueryParams, err = resolveDatasourceVariable(ctx, *args.DashboardUID, *args.DatasourceUID, args.QueryParams)
			if err != nil {
				return "", err
			}
		}
		deeplink = fmt.Sprintf("%s/d/%s?viewPanel=%d", baseURL, *args.DashboardUID, *args.PanelID)
	case "explore":
		if args.DatasourceUID == nil {
			return "", fmt.Errorf("datasourceUid is required for explore links")
		}
		leftState, err := buildExploreLeftParam(*args.DatasourceUID, args.Queries, args.TimeRange)
		if err != nil {
			return "", fmt.Errorf("failed to build explore state: %w", err)
		}
		params := url.Values{}
		params.Set("left", leftState)
		deeplink = fmt.Sprintf("%s/explore?%s", baseURL, params.Encode())
	default:
		return "", fmt.Errorf("unsupported resource type: %s. Supported types are: dashboard, panel, explore", args.ResourceType)
	}

	// For non-explore types, append time range as top-level URL params
	if strings.ToLower(args.ResourceType) != "explore" && args.TimeRange != nil {
		separator := "?"
		if strings.Contains(deeplink, "?") {
			separator = "&"
		}
		timeParams := url.Values{}
		if args.TimeRange.From != "" {
			timeParams.Set("from", args.TimeRange.From)
		}
		if args.TimeRange.To != "" {
			timeParams.Set("to", args.TimeRange.To)
		}
		if len(timeParams) > 0 {
			deeplink = fmt.Sprintf("%s%s%s", deeplink, separator, timeParams.Encode())
		}
	}

	if len(args.QueryParams) > 0 {
		separator := "?"
		if strings.Contains(deeplink, "?") {
			separator = "&"
		}
		additionalParams := url.Values{}
		for key, value := range args.QueryParams {
			additionalParams.Set(key, value)
		}
		deeplink = fmt.Sprintf("%s%s%s", deeplink, separator, additionalParams.Encode())
	}

	return deeplink, nil
}

var GenerateDeeplink = mcpgrafana.MustTool(
	"generate_deeplink",
	"Generate deeplink URLs for Grafana resources. Supports dashboards (requires dashboardUid), panels (requires dashboardUid and panelId), and Explore queries (requires datasourceUid). For dashboard/panel links, providing datasourceUid auto-resolves the datasource template variable. Optionally accepts time range (use relative like 'now-1h' or epoch ms, NOT ISO timestamps) and additional query parameters.",
	generateDeeplink,
	mcp.WithTitleAnnotation("Generate navigation deeplink"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddNavigationTools(mcp *server.MCPServer) {
	GenerateDeeplink.Register(mcp)
}
