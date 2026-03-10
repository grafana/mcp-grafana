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
	// ExploreQuery is an optional PromQL/LogQL query expression for explore links
	ExploreQuery *string `json:"exploreQuery,omitempty" jsonschema:"description=Query expression (e.g. PromQL or LogQL) for explore links"`
	// UseLegacyExploreURL forces use of the legacy explore URL format (left= parameter).
	// By default\, the new schemaVersion=1 format is used for Grafana 10+.
	UseLegacyExploreURL *bool `json:"useLegacyExploreUrl,omitempty" jsonschema:"description=Force legacy explore URL format (left= parameter). Default uses new schemaVersion=1 format."`
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

		// Determine whether to use legacy or new URL format
		useLegacy := false
		if args.UseLegacyExploreURL != nil {
			useLegacy = *args.UseLegacyExploreURL
		}

		if useLegacy {
			// Legacy format: /explore?left={"datasource":"uid", "queries":[...], "range":{...}}
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

			// For legacy explore, time range is already embedded in `left` — skip the
			// generic time range block below by clearing it.
			args.TimeRange = nil
		} else {
			// New format (Grafana 10+): /explore?schemaVersion=1&panes={...}
			// Time range is embedded in the panes JSON, so we pass it here
			// and clear it to avoid double-encoding as query params below.
			var err error
			deeplink, err = generateExploreDeeplinkNew(baseURL, *args.DatasourceUID, args.ExploreQuery, args.TimeRange)
			if err != nil {
				return "", err
			}
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
	"Generate deeplink URLs for Grafana resources. Supports dashboards (requires dashboardUid), panels (requires dashboardUid and panelId), and Explore queries (requires datasourceUid and optionally queries). For explore links, the time range and queries are embedded inside the Grafana explore state.",
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
func generateExploreDeeplinkNew(baseURL, datasourceUID string, query *string, timeRange *TimeRange) (string, error) {
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
	panesJSON, err := json.Marshal(panes)
	if err != nil {
		return "", fmt.Errorf("marshal explore panes: %w", err)
	}

	// Build the URL
	params := url.Values{}
	params.Set("schemaVersion", "1")
	params.Set("panes", string(panesJSON))

	return fmt.Sprintf("%s/explore?%s", baseURL, params.Encode()), nil
}
