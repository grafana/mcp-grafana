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
	ResourceType  string            `json:"resourceType" jsonschema:"required,description=Type of resource: dashboard\\, panel\\, or explore"`
	DashboardUID  *string           `json:"dashboardUid,omitempty" jsonschema:"description=Dashboard UID (required for dashboard and panel types)"`
	DatasourceUID *string           `json:"datasourceUid,omitempty" jsonschema:"description=Datasource UID (required for explore type)"`
	PanelID       *int              `json:"panelId,omitempty" jsonschema:"description=Panel ID (required for panel type)"`
	QueryParams   map[string]string `json:"queryParams,omitempty" jsonschema:"description=Additional query parameters"`
	TimeRange     *TimeRange        `json:"timeRange,omitempty" jsonschema:"description=Time range for the link"`
	// ExploreQuery is an optional PromQL/LogQL query expression for explore links
	ExploreQuery *string `json:"exploreQuery,omitempty" jsonschema:"description=Query expression (e.g. PromQL or LogQL) for explore links"`
	// UseLegacyExploreURL forces use of the legacy explore URL format (left= parameter).
	// By default\\, the new schemaVersion=1 format is used for Grafana 10+.
	UseLegacyExploreURL *bool `json:"useLegacyExploreUrl,omitempty" jsonschema:"description=Force legacy explore URL format (left= parameter). Default uses new schemaVersion=1 format."`
}

type TimeRange struct {
	From string `json:"from" jsonschema:"description=Start time (e.g.\\, 'now-1h')"`
	To   string `json:"to" jsonschema:"description=End time (e.g.\\, 'now')"`
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

		// Determine whether to use legacy or new URL format
		useLegacy := false
		if args.UseLegacyExploreURL != nil {
			useLegacy = *args.UseLegacyExploreURL
		}

		if useLegacy {
			// Legacy format: /explore?left={"datasource":"uid"}
			params := url.Values{}
			exploreState := fmt.Sprintf(`{"datasource":"%s"}`, *args.DatasourceUID)
			params.Set("left", exploreState)
			deeplink = fmt.Sprintf("%s/explore?%s", baseURL, params.Encode())
		} else {
			// New format (Grafana 10+): /explore?schemaVersion=1&panes={...}
			// Time range is embedded in the panes JSON, so we pass it here and skip
			// adding it as query params below
			deeplink = generateExploreDeeplinkNew(baseURL, *args.DatasourceUID, args.ExploreQuery, args.TimeRange)
			// Clear time range to avoid double-encoding (it's already in the panes JSON)
			args.TimeRange = nil
		}
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
	"Generate deeplink URLs for Grafana resources. Supports dashboards (requires dashboardUid), panels (requires dashboardUid and panelId), and Explore queries (requires datasourceUid). Optionally accepts time range and additional query parameters.",
	generateDeeplink,
	mcp.WithTitleAnnotation("Generate navigation deeplink"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddNavigationTools(mcp *server.MCPServer) {
	GenerateDeeplink.Register(mcp)
}

// explorePane represents a single pane in the explore view for the new URL format
type explorePane struct {
	Datasource string           `json:"datasource"`
	Queries    []exploreQuery   `json:"queries"`
	Range      explorePaneRange `json:"range"`
}

// exploreQuery represents a query within an explore pane
type exploreQuery struct {
	Refid      string `json:"refId"`
	Datasource struct {
		UID  string `json:"uid"`
		Type string `json:"type,omitempty"`
	} `json:"datasource"`
	Expr string `json:"expr,omitempty"`
}

// explorePaneRange represents the time range for an explore pane
type explorePaneRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// generateExploreDeeplinkNew creates an explore URL using the new schemaVersion=1 format (Grafana 10+)
func generateExploreDeeplinkNew(baseURL, datasourceUID string, query *string, timeRange *TimeRange) string {
	// Build the query object
	q := exploreQuery{
		Refid: "A",
	}
	q.Datasource.UID = datasourceUID
	if query != nil {
		q.Expr = *query
	}

	// Use provided time range or default to now-1h/now
	paneRange := explorePaneRange{
		From: "now-1h",
		To:   "now",
	}
	if timeRange != nil {
		if timeRange.From != "" {
			paneRange.From = timeRange.From
		}
		if timeRange.To != "" {
			paneRange.To = timeRange.To
		}
	}

	// Build the pane
	pane := explorePane{
		Datasource: datasourceUID,
		Queries:    []exploreQuery{q},
		Range:      paneRange,
	}

	// Build the panes object with a single pane
	panes := map[string]explorePane{
		"pane1": pane,
	}

	// Encode to JSON
	panesJSON, _ := json.Marshal(panes)

	// Build the URL
	params := url.Values{}
	params.Set("schemaVersion", "1")
	params.Set("panes", string(panesJSON))

	return fmt.Sprintf("%s/explore?%s", baseURL, params.Encode())
}
