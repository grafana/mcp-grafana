package tools

import (
	"context"
	"fmt"
	"strings"

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

// Params for the new tool
// PanelExprInfo holds a panel title and its expressions
type PanelExprInfo struct {
	Title string   `json:"title"`
	Exprs []string `json:"exprs"`
}

type GetDashboardPanelTitlesAndExprsParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
}

type GetDashboardPanelTitlesAndExprsResult struct {
	Result []PanelExprInfo `json:"result"`
}

type StringResult struct {
	Result string `json:"result"`
}

// Tool function
func getDashboardPanelExprsText(ctx context.Context, args GetDashboardPanelTitlesAndExprsParams) (StringResult, error) {
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams(args))
	if err != nil {
		return StringResult{}, fmt.Errorf("get dashboard by uid: %w", err)
	}

	queries, err := jsonpath.Get("$.panels[*].targets[*].expr", dashboard.Dashboard)
	if err != nil {
		return StringResult{
			Result: fmt.Sprintf("jsonpath error (exprs): %s", err),
		}, fmt.Errorf("jsonpath error (exprs): %w", err)
	}

	var exprs []string
	switch q := queries.(type) {
	case []interface{}:
		for _, expr := range q {
			if s, ok := expr.(string); ok {
				exprs = append(exprs, s)
			}
		}
	default:
		return StringResult{}, fmt.Errorf("unexpected type: %T", q)
	}

	return StringResult{Result: strings.Join(exprs, "\n")}, nil
}

// Register the tool
var GetDashboardPanelExprsText = mcpgrafana.MustTool(
	"get_dashboard_panel_exprs_text",
	"Returns all panel expressions for a dashboard as plain text, one per line.",
	getDashboardPanelExprsText,
)

func AddDashboardTools(mcp *server.MCPServer) {
	GetDashboardByUID.Register(mcp)
	UpdateDashboard.Register(mcp)
	GetDashboardPanelExprsText.Register(mcp)
}
