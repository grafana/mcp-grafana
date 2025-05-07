package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/server"

	"github.com/PaesslerAG/jsonpath"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

type GetDashboardByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
}

func getDashboardByUID(ctx context.Context, args GetDashboardByUIDParams) (*models.DashboardFullWithMeta, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	dashboard, err := c.Dashboards.GetDashboardByUID(args.UID)
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid %s: %w", args.UID, err)
	}
	return dashboard.Payload, nil
}

type UpdateDashboardParams struct {
	Dashboard map[string]interface{} `json:"dashboard" jsonschema:"required,description=The full dashboard JSON"`
	FolderUID string                 `json:"folderUid" jsonschema:"optional,description=The UID of the dashboard's folder"`
	Message   string                 `json:"message" jsonschema:"optional,description=Set a commit message for the version history"`
	Overwrite bool                   `json:"overwrite" jsonschema:"optional,description=Overwrite the dashboard if it exists. Otherwise create one"`
	UserID    int64                  `json:"userId" jsonschema:"optional,ID of the user making the change"`
}

// updateDashboard can be used to save an existing dashboard, or create a new one.
// DISCLAIMER: Large-sized dashboard JSON can exhaust context windows. We will
// implement features that address this in https://github.com/grafana/mcp-grafana/issues/101.
func updateDashboard(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	cmd := &models.SaveDashboardCommand{
		Dashboard: args.Dashboard,
		FolderUID: args.FolderUID,
		Message:   args.Message,
		Overwrite: args.Overwrite,
		UserID:    args.UserID,
	}
	dashboard, err := c.Dashboards.PostDashboard(cmd)
	if err != nil {
		return nil, fmt.Errorf("unable to save dashboard: %w", err)
	}
	return dashboard.Payload, nil
}

var GetDashboardByUID = mcpgrafana.MustTool(
	"get_dashboard_by_uid",
	"Retrieves the complete dashboard, including panels, variables, and settings, for a specific dashboard identified by its UID.",
	getDashboardByUID,
)

var UpdateDashboard = mcpgrafana.MustTool(
	"update_dashboard",
	"Create or update a dashboard",
	updateDashboard,
)

type DashboardPanelQueriesParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
}

type PanelQuery struct {
	Title         string `json:"title"`
	Query         string `json:"query"`
	DatasourceUID string `json:"datasourceUid"`
}

func GetDashboardPanelQueriesTool(ctx context.Context, args DashboardPanelQueriesParams) ([]PanelQuery, error) {
	result := make([]PanelQuery, 0)

	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams(args))
	if err != nil {
		return result, fmt.Errorf("get dashboard by uid: %w", err)
	}

	panelsResult, err := jsonpath.Get("$.panels[*]", dashboard.Dashboard)
	if err != nil {
		return result, fmt.Errorf("jsonpath error (panels): %w", err)
	}

	panels, ok := panelsResult.([]any)
	if !ok {
		return result, fmt.Errorf("panels is not a slice")
	}

	for _, p := range panels {
		panel, ok := p.(map[string]any)
		if !ok {
			continue
		}
		title, _ := panel["title"].(string)

		var datasourceUid string
		if dsField, dsExists := panel["datasource"]; dsExists && dsField != nil {
			if dsMap, ok := dsField.(map[string]any); ok {
				if uid, ok := dsMap["uid"].(string); ok {
					datasourceUid = uid
				}
			}
		}

		targets, ok := panel["targets"].([]any)
		if !ok {
			continue
		}
		for _, t := range targets {
			target, ok := t.(map[string]any)
			if !ok {
				continue
			}
			expr, _ := target["expr"].(string)
			if expr != "" {
				result = append(result, PanelQuery{
					Title:         title,
					Query:         expr,
					DatasourceUID: datasourceUid,
				})
			}
		}
	}

	return result, nil
}

var GetDashboardPanelQueries = mcpgrafana.MustTool(
	"get_dashboard_panel_queries",
	"Get the title, datasource UID (which may be a concrete UID or a template variable like \"$datasource\"), and query string of each panel in a dashboard. If the datasource UID is a template variable, it won't be usable directly for queries. Returns an array of objects, each representing a panel in the specified dashboard.",
	GetDashboardPanelQueriesTool,
)

func AddDashboardTools(mcp *server.MCPServer) {
	GetDashboardByUID.Register(mcp)
	UpdateDashboard.Register(mcp)
	GetDashboardPanelQueries.Register(mcp)
}
