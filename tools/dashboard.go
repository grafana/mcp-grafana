package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
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

// smartUpdateDashboard intelligently updates a dashboard by checking if it's provisioned
// If provisioned, it uses file management; otherwise it uses direct dashboard API
func smartUpdateDashboard(ctx context.Context, args UpdateDashboardParams) (string, error) {
	// First check if dashboard exists and get its UID
	dashboardMap := args.Dashboard

	uid, _ := dashboardMap["uid"].(string)
	if uid == "" {
		// New dashboard, use regular update
		result, err := updateDashboard(ctx, args)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Dashboard created successfully:\n- UID: %s\n- URL: %s\n- Version: %d",
			*result.UID, *result.URL, *result.Version), nil
	}

	// Check if dashboard is provisioned
	managerInfo, err := getDashboardManager(ctx, GetDashboardManagerParams{ID: uid})
	if err != nil {
		// If we can't get manager info, fall back to regular update
		result, err := updateDashboard(ctx, args)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Dashboard updated successfully:\n- UID: %s\n- URL: %s\n- Version: %d",
			*result.UID, *result.URL, *result.Version), nil
	}

	// Check if the dashboard is managed by GitOps
	if strings.Contains(managerInfo, "managedBy:") && strings.Contains(managerInfo, "managerId:") && strings.Contains(managerInfo, "sourcePath:") {
		// Dashboard is provisioned - extract manager details
		lines := strings.Split(managerInfo, "\n")
		var managerId, sourcePath string

		for _, line := range lines {
			if strings.HasPrefix(line, "- managerId:") {
				managerId = strings.TrimSpace(strings.TrimPrefix(line, "- managerId:"))
			}
			if strings.HasPrefix(line, "- sourcePath:") {
				sourcePath = strings.TrimSpace(strings.TrimPrefix(line, "- sourcePath:"))
			}
		}

		if managerId == "" || sourcePath == "" {
			return "", fmt.Errorf("could not extract manager details from provisioned dashboard")
		}

		// Store dashboard as plain JSON (not wrapped in Kubernetes resource)
		// The GitOps controller will handle the Kubernetes resource wrapping
		contentBytes, err := json.MarshalIndent(dashboardMap, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal dashboard JSON: %w", err)
		}

		// Use file management to update the provisioned dashboard
		fileResult, err := manageProvisioningRepositoryFile(ctx, ManageProvisioningRepositoryFileParams{
			RepositoryName: managerId,
			Path:           sourcePath,
			Operation:      "update",
			Content:        string(contentBytes),
			Message:        args.Message,
		})

		if err != nil {
			return "", fmt.Errorf("failed to update provisioned dashboard via file management: %w", err)
		}

		return fmt.Sprintf("✅ Provisioned dashboard updated via GitOps:\n- Repository: %s\n- File: %s\n- UID: %s\n\nFile management result:\n%s",
			managerId, sourcePath, uid, fileResult), nil
	}

	// Dashboard is not provisioned, use regular update
	result, err := updateDashboard(ctx, args)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Dashboard updated successfully:\n- UID: %s\n- URL: %s\n- Version: %d",
		*result.UID, *result.URL, *result.Version), nil
}

var GetDashboardByUID = mcpgrafana.MustTool(
	"get_dashboard_by_uid",
	"Retrieves the complete dashboard, including panels, variables, and settings, for a specific dashboard identified by its UID.",
	getDashboardByUID,
	mcp.WithTitleAnnotation("Get dashboard details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var UpdateDashboard = mcpgrafana.MustTool(
	"update_dashboard",
	"Create or update a dashboard",
	updateDashboard,
	mcp.WithTitleAnnotation("Create or update dashboard"),
	mcp.WithDestructiveHintAnnotation(true),
)

var SmartUpdateDashboard = mcpgrafana.MustTool(
	"smart_update_dashboard",
	`Smart dashboard update that automatically detects if a dashboard is provisioned.

	IMPORTANT: This tool automatically determines the correct update method:
	- For NEW dashboards: Uses standard Grafana API
	- For EXISTING non-provisioned dashboards: Uses standard Grafana API  
	- For EXISTING provisioned dashboards: Uses GitOps file management

	GitOps Integration:
	When updating provisioned dashboards, this tool:
	1. Detects the dashboard is managed by GitOps
	2. Extracts repository and file path information
	3. Stores dashboard as plain JSON in the repository
	4. Updates the source file in the Git repository
	5. Commits changes with your message
	6. Allows GitSync to apply changes to Grafana

	This ensures that:
	- Provisioned dashboards remain under version control
	- Changes are tracked in Git history
	- Configuration drift is prevented
	- Team collaboration workflows are maintained

	Use this tool for ALL dashboard updates to ensure proper GitOps compliance.`,
	smartUpdateDashboard,
	mcp.WithTitleAnnotation("Smart dashboard create/update with GitOps support"),
	mcp.WithDestructiveHintAnnotation(true),
)

type DashboardPanelQueriesParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
}

type datasourceInfo struct {
	UID  string `json:"uid"`
	Type string `json:"type"`
}

type panelQuery struct {
	Title      string         `json:"title"`
	Query      string         `json:"query"`
	Datasource datasourceInfo `json:"datasource"`
}

func GetDashboardPanelQueriesTool(ctx context.Context, args DashboardPanelQueriesParams) ([]panelQuery, error) {
	result := make([]panelQuery, 0)

	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams(args))
	if err != nil {
		return result, fmt.Errorf("get dashboard by uid: %w", err)
	}

	db, ok := dashboard.Dashboard.(map[string]any)
	if !ok {
		return result, fmt.Errorf("dashboard is not a JSON object")
	}
	panels, ok := db["panels"].([]any)
	if !ok {
		return result, fmt.Errorf("panels is not a JSON array")
	}

	for _, p := range panels {
		panel, ok := p.(map[string]any)
		if !ok {
			continue
		}
		title, _ := panel["title"].(string)

		var datasourceInfo datasourceInfo
		if dsField, dsExists := panel["datasource"]; dsExists && dsField != nil {
			if dsMap, ok := dsField.(map[string]any); ok {
				if uid, ok := dsMap["uid"].(string); ok {
					datasourceInfo.UID = uid
				}
				if dsType, ok := dsMap["type"].(string); ok {
					datasourceInfo.Type = dsType
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
				result = append(result, panelQuery{
					Title:      title,
					Query:      expr,
					Datasource: datasourceInfo,
				})
			}
		}
	}

	return result, nil
}

var GetDashboardPanelQueries = mcpgrafana.MustTool(
	"get_dashboard_panel_queries",
	"Get the title, query string, and datasource information for each panel in a dashboard. The datasource is an object with fields `uid` (which may be a concrete UID or a template variable like \"$datasource\") and `type`. If the datasource UID is a template variable, it won't be usable directly for queries. Returns an array of objects, each representing a panel, with fields: title, query, and datasource (an object with uid and type).",
	GetDashboardPanelQueriesTool,
	mcp.WithTitleAnnotation("Get dashboard panel queries"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddDashboardTools(mcp *server.MCPServer) {
	GetDashboardByUID.Register(mcp)
	UpdateDashboard.Register(mcp)
	SmartUpdateDashboard.Register(mcp)
	GetDashboardPanelQueries.Register(mcp)
}

func AddGetDashboardManagerTool(mcp *server.MCPServer) {
	GetDashboardManager.Register(mcp)
}

const getDashboardManagerToolPrompt = `Retrieves dashboard manager details to understand how a dashboard is managed and where its source files are located.

This tool returns information about the GitOps manager responsible for a dashboard, including:
- Manager type (e.g. "repo")
- Manager ID (e.g. "repo-123")
- Source path within the repository

Use this information to:
- Locate the source files for a dashboard
- Understand the GitOps workflow for the dashboard
- Determine the repository structure and file organization
- Access the version-controlled configuration files

The returned details help you navigate to the exact location of dashboard files by combining the repository URL, branch, and target path with the specific resource path.`

var GetDashboardManager = mcpgrafana.MustTool(
	"get_dashboard_manager",
	getDashboardManagerToolPrompt,
	getDashboardManager,
	mcp.WithTitleAnnotation("Get Dashboard Manager Details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type GetDashboardManagerParams struct {
	ID string `json:"id" jsonschema:"required,description=The id of the dashboard manager to get"`
}

type DashboardManagerResponse struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

func getDashboardManager(ctx context.Context, args GetDashboardManagerParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Construct the API URL for the dashboard manager
	apiPath := fmt.Sprintf("/apis/dashboard.grafana.app/v2alpha1/namespaces/default/dashboards/%s", args.ID)
	url := fmt.Sprintf("%s%s", strings.TrimRight(cfg.URL, "/"), apiPath)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	// Add authorization header
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	} else if cfg.AccessToken != "" && cfg.IDToken != "" {
		req.Header.Set("X-Access-Token", cfg.AccessToken)
		req.Header.Set("X-Grafana-Id", cfg.IDToken)
	}

	// Create HTTP client with TLS configuration if available
	client := &http.Client{}
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		transport, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
		if err != nil {
			return "", fmt.Errorf("failed to create custom transport: %w", err)
		}
		client.Transport = transport
	}

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Sprintf("No dashboard manager found for id: %s", args.ID), nil
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response DashboardManagerResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if response.Metadata.Annotations == nil {
		return fmt.Sprintf("No annotations found for dashboard %s", args.ID), nil
	}

	managedBy := response.Metadata.Annotations["grafana.app/managedBy"]
	managerID := response.Metadata.Annotations["grafana.app/managerId"]
	sourcePath := response.Metadata.Annotations["grafana.app/sourcePath"]

	if managedBy == "" || managerID == "" || sourcePath == "" {
		return fmt.Sprintf("No manager annotations found for dashboard %s", args.ID), nil
	}

	result := fmt.Sprintf("This dashboard is managed:\n- managedBy: %s\n- managerId: %s\n- sourcePath: %s",
		managedBy, managerID, sourcePath)

	return result, nil
}
