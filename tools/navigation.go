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
)

type GenerateDeeplinkParams struct {
	ResourceType  string                   `json:"resourceType" jsonschema:"required,description=Type of resource: dashboard\\, panel\\, or explore"`
	DashboardUID  *string                  `json:"dashboardUid,omitempty" jsonschema:"description=Dashboard UID (required for dashboard and panel types)"`
	DatasourceUID *string                  `json:"datasourceUid,omitempty" jsonschema:"description=Datasource UID (required for explore type)"`
	PanelID       *int                     `json:"panelId,omitempty" jsonschema:"description=Panel ID (required for panel type)"`
	Queries       []map[string]interface{} `json:"queries,omitempty" jsonschema:"description=List of query objects for explore links (e.g. [{\"refId\":\"A\"\\,\"expr\":\"up\"}])"`
	QueryParams   map[string]string        `json:"queryParams,omitempty" jsonschema:"description=Additional URL query parameters (for dashboard/panel types)"`
	TimeRange     *TimeRange               `json:"timeRange,omitempty" jsonschema:"description=Time range for the link"`
}

type TimeRange struct {
	From string `json:"from" jsonschema:"description=Start time (e.g.\\, 'now-1h')"`
	To   string `json:"to" jsonschema:"description=End time (e.g.\\, 'now')"`
}

func generateDeeplink(ctx context.Context, args GenerateDeeplinkParams) (string, error) {
	// Prefer the public URL from the Grafana client (fetched from /api/frontend/settings),
	// falling back to the configured URL if the client is not available or has no public URL.
	var baseURL string
	if gc := mcpgrafana.GrafanaClientFromContext(ctx); gc != nil && gc.PublicURL != "" {
		baseURL = gc.PublicURL
	} else {
		config := mcpgrafana.GrafanaConfigFromContext(ctx)
		baseURL = strings.TrimRight(config.URL, "/")
	}

	if baseURL == "" {
		return "", fmt.Errorf("grafana url not configured. Please set GRAFANA_URL environment variable or X-Grafana-URL header")
	}

	var deeplink string

	switch strings.ToLower(args.ResourceType) {
	case "dashboard":
		if args.DashboardUID == nil {
			return "", fmt.Errorf("dashboardUid is required for dashboard links")
		}
		deeplink = fmt.Sprintf("%s/d/%s", baseURL, *args.DashboardUID)

	case "panel":
		if args.DashboardUID == nil {
			return "", fmt.Errorf("dashboardUid is required for panel links")
		}
		if args.PanelID == nil {
			return "", fmt.Errorf("panelId is required for panel links")
		}
		deeplink = fmt.Sprintf("%s/d/%s?viewPanel=%d", baseURL, *args.DashboardUID, *args.PanelID)

	case "explore":
		if args.DatasourceUID == nil {
			return "", fmt.Errorf("datasourceUid is required for explore links")
		}

		// Build the full explore state inside `left` — Grafana Explore reads
		// datasource, queries, and range all from this single JSON object.
		exploreState := map[string]interface{}{
			"datasource": *args.DatasourceUID,
		}
		if len(args.Queries) > 0 {
			exploreState["queries"] = args.Queries
		}
		if args.TimeRange != nil {
			rangeObj := map[string]string{}
			if args.TimeRange.From != "" {
				rangeObj["from"] = args.TimeRange.From
			}
			if args.TimeRange.To != "" {
				rangeObj["to"] = args.TimeRange.To
			}
			if len(rangeObj) > 0 {
				exploreState["range"] = rangeObj
			}
		}

		leftJSON, err := json.Marshal(exploreState)
		if err != nil {
			return "", fmt.Errorf("failed to marshal explore state: %w", err)
		}

		params := url.Values{}
		params.Set("left", string(leftJSON))
		deeplink = fmt.Sprintf("%s/explore?%s", baseURL, params.Encode())

		// For explore, time range is already embedded in `left` — skip the
		// generic time range block below by clearing it.
		args.TimeRange = nil

	default:
		return "", fmt.Errorf("unsupported resource type: %s. Supported types are: dashboard, panel, explore", args.ResourceType)
	}

	if args.TimeRange != nil {
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
	"Generate deeplink URLs for Grafana resources. Supports dashboards (requires dashboardUid), panels (requires dashboardUid and panelId), and Explore queries (requires datasourceUid and optionally queries). For explore links, the time range and queries are embedded inside the Grafana explore state.",
	generateDeeplink,
	mcp.WithTitleAnnotation("Generate navigation deeplink"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddNavigationTools(mcp *server.MCPServer) {
	GenerateDeeplink.Register(mcp)
}
