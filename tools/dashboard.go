package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/server"

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

var GetDashboardByUID = mcpgrafana.MustTool(
	"get_dashboard_by_uid",
	"Get dashboard by uid",
	getDashboardByUID,
)

func AddDashboardTools(mcp *server.MCPServer) {
	GetDashboardByUID.Register(mcp)
}