package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/oliveagle/jsonpath"

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

// PatchOperation represents a single patch operation
type PatchOperation struct {
	Op    string      `json:"op" jsonschema:"required,description=Operation type: 'replace'\\, 'add'\\, 'remove'"`
	Path  string      `json:"path" jsonschema:"required,description=JSONPath to the property to modify. Supports: '$.title'\\, '$.panels[0].title'\\, '$.panels[0].targets[0].expr'\\, '$.panels[1].targets[0].datasource'\\, etc."`
	Value interface{} `json:"value,omitempty" jsonschema:"description=New value for replace/add operations"`
}

type UpdateDashboardParams struct {
	// For full dashboard updates (creates new dashboards or complete rewrites)
	Dashboard map[string]interface{} `json:"dashboard,omitempty" jsonschema:"description=The full dashboard JSON. Use for creating new dashboards or complete updates. Large dashboards consume significant context - consider using patches for small changes."`

	// For targeted updates using patch operations (preferred for existing dashboards)
	UID        string           `json:"uid,omitempty" jsonschema:"description=UID of existing dashboard to update. Required when using patch operations."`
	Operations []PatchOperation `json:"operations,omitempty" jsonschema:"description=Array of patch operations for targeted updates. More efficient than full dashboard JSON for small changes."`

	// Common parameters
	FolderUID string `json:"folderUid,omitempty" jsonschema:"description=The UID of the dashboard's folder"`
	Message   string `json:"message,omitempty" jsonschema:"description=Set a commit message for the version history"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"description=Overwrite the dashboard if it exists. Otherwise create one"`
	UserID    int64  `json:"userId,omitempty" jsonschema:"description=ID of the user making the change"`
}

// updateDashboard intelligently handles dashboard updates using either full JSON or patch operations.
// It automatically uses the most efficient approach based on the provided parameters.
func updateDashboard(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	// Determine the update strategy based on provided parameters
	if len(args.Operations) > 0 && args.UID != "" {
		// Patch-based update: fetch current dashboard and apply operations
		return updateDashboardWithPatches(ctx, args)
	} else if args.Dashboard != nil {
		// Full dashboard update: use the provided JSON
		return updateDashboardWithFullJSON(ctx, args)
	} else {
		return nil, fmt.Errorf("either dashboard JSON or (uid + operations) must be provided")
	}
}

// updateDashboardWithPatches applies patch operations to an existing dashboard
func updateDashboardWithPatches(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	// Get the current dashboard
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.UID})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	// Convert to modifiable map
	dashboardMap, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	// Apply each patch operation
	for i, op := range args.Operations {
		switch op.Op {
		case "replace", "add":
			if err := setValueAtPath(dashboardMap, op.Path, op.Value); err != nil {
				return nil, fmt.Errorf("operation %d (%s at %s): %w", i, op.Op, op.Path, err)
			}
		case "remove":
			if err := removeValueAtPath(dashboardMap, op.Path); err != nil {
				return nil, fmt.Errorf("operation %d (%s at %s): %w", i, op.Op, op.Path, err)
			}
		default:
			return nil, fmt.Errorf("operation %d: unsupported operation '%s'", i, op.Op)
		}
	}

	// Use the folder UID from the existing dashboard if not provided
	folderUID := args.FolderUID
	if folderUID == "" && dashboard.Meta != nil {
		folderUID = dashboard.Meta.FolderUID
	}

	// Update with the patched dashboard
	return updateDashboardWithFullJSON(ctx, UpdateDashboardParams{
		Dashboard: dashboardMap,
		FolderUID: folderUID,
		Message:   args.Message,
		Overwrite: true, // Always overwrite when patching
		UserID:    args.UserID,
	})
}

// updateDashboardWithFullJSON performs a traditional full dashboard update
func updateDashboardWithFullJSON(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
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
	"Retrieves the complete dashboard, including panels, variables, and settings, for a specific dashboard identified by its UID. WARNING: Large dashboards can consume significant context window space. Consider using get_dashboard_summary for overview or get_dashboard_property for specific data instead.",
	getDashboardByUID,
	mcp.WithTitleAnnotation("Get dashboard details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var UpdateDashboard = mcpgrafana.MustTool(
	"update_dashboard",
	"Create or update a dashboard using either full JSON or efficient patch operations. For new dashboards\\, provide the 'dashboard' field. For updating existing dashboards\\, use 'uid' + 'operations' for better context window efficiency. Patch operations support complex JSONPaths like '$.panels[0].targets[0].expr'\\, '$.panels[1].title'\\, '$.panels[2].targets[0].datasource'\\, etc.",
	updateDashboard,
	mcp.WithTitleAnnotation("Create or update dashboard"),
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

// GetDashboardPropertyParams defines parameters for getting specific dashboard properties
type GetDashboardPropertyParams struct {
	UID      string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
	JSONPath string `json:"jsonPath" jsonschema:"required,description=JSONPath expression to extract specific data (e.g.\\, '$.panels[0].title' for first panel title\\, '$.panels[*].title' for all panel titles\\, '$.templating.list' for variables)"`
}

// getDashboardProperty retrieves specific parts of a dashboard using JSONPath expressions.
// This helps reduce context window usage by fetching only the needed data.
func getDashboardProperty(ctx context.Context, args GetDashboardPropertyParams) (interface{}, error) {
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.UID})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	// Convert dashboard to JSON for JSONPath processing
	dashboardJSON, err := json.Marshal(dashboard.Dashboard)
	if err != nil {
		return nil, fmt.Errorf("marshal dashboard to JSON: %w", err)
	}

	var dashboardData interface{}
	if err := json.Unmarshal(dashboardJSON, &dashboardData); err != nil {
		return nil, fmt.Errorf("unmarshal dashboard JSON: %w", err)
	}

	// Apply JSONPath expression
	result, err := jsonpath.JsonPathLookup(dashboardData, args.JSONPath)
	if err != nil {
		return nil, fmt.Errorf("apply JSONPath '%s': %w", args.JSONPath, err)
	}

	return result, nil
}

var GetDashboardProperty = mcpgrafana.MustTool(
	"get_dashboard_property",
	"Get specific parts of a dashboard using JSONPath expressions to minimize context window usage. Common paths: '$.title' (title)\\, '$.panels[*].title' (all panel titles)\\, '$.panels[0]' (first panel)\\, '$.templating.list' (variables)\\, '$.tags' (tags)\\, '$.panels[*].targets[*].expr' (all queries). Use this instead of get_dashboard_by_uid when you only need specific dashboard properties.",
	getDashboardProperty,
	mcp.WithTitleAnnotation("Get dashboard property"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// GetDashboardSummaryParams defines parameters for getting a dashboard summary
type GetDashboardSummaryParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
}

// DashboardSummary provides a compact overview of a dashboard without the full JSON
type DashboardSummary struct {
	UID         string                `json:"uid"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	PanelCount  int                   `json:"panelCount"`
	Panels      []PanelSummary        `json:"panels"`
	Variables   []VariableSummary     `json:"variables,omitempty"`
	TimeRange   TimeRangeSummary      `json:"timeRange"`
	Refresh     string                `json:"refresh,omitempty"`
	Meta        *models.DashboardMeta `json:"meta,omitempty"`
}

type PanelSummary struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	QueryCount  int    `json:"queryCount"`
}

type VariableSummary struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
}

type TimeRangeSummary struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// getDashboardSummary provides a compact overview of a dashboard to help with context management
func getDashboardSummary(ctx context.Context, args GetDashboardSummaryParams) (*DashboardSummary, error) {
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.UID})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	db, ok := dashboard.Dashboard.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	summary := &DashboardSummary{
		UID:  args.UID,
		Meta: dashboard.Meta,
	}

	// Extract basic info
	if title, ok := db["title"].(string); ok {
		summary.Title = title
	}
	if desc, ok := db["description"].(string); ok {
		summary.Description = desc
	}
	if tags, ok := db["tags"].([]interface{}); ok {
		for _, tag := range tags {
			if tagStr, ok := tag.(string); ok {
				summary.Tags = append(summary.Tags, tagStr)
			}
		}
	}
	if refresh, ok := db["refresh"].(string); ok {
		summary.Refresh = refresh
	}

	// Extract time range
	if timeObj, ok := db["time"].(map[string]interface{}); ok {
		if from, ok := timeObj["from"].(string); ok {
			summary.TimeRange.From = from
		}
		if to, ok := timeObj["to"].(string); ok {
			summary.TimeRange.To = to
		}
	}

	// Extract panel summaries
	if panels, ok := db["panels"].([]interface{}); ok {
		summary.PanelCount = len(panels)
		for _, p := range panels {
			if panel, ok := p.(map[string]interface{}); ok {
				panelSummary := PanelSummary{}

				if id, ok := panel["id"].(float64); ok {
					panelSummary.ID = int(id)
				}
				if title, ok := panel["title"].(string); ok {
					panelSummary.Title = title
				}
				if panelType, ok := panel["type"].(string); ok {
					panelSummary.Type = panelType
				}
				if desc, ok := panel["description"].(string); ok {
					panelSummary.Description = desc
				}

				// Count queries
				if targets, ok := panel["targets"].([]interface{}); ok {
					panelSummary.QueryCount = len(targets)
				}

				summary.Panels = append(summary.Panels, panelSummary)
			}
		}
	}

	// Extract variable summaries
	if templating, ok := db["templating"].(map[string]interface{}); ok {
		if list, ok := templating["list"].([]interface{}); ok {
			for _, v := range list {
				if variable, ok := v.(map[string]interface{}); ok {
					varSummary := VariableSummary{}

					if name, ok := variable["name"].(string); ok {
						varSummary.Name = name
					}
					if varType, ok := variable["type"].(string); ok {
						varSummary.Type = varType
					}
					if label, ok := variable["label"].(string); ok {
						varSummary.Label = label
					}

					summary.Variables = append(summary.Variables, varSummary)
				}
			}
		}
	}

	return summary, nil
}

var GetDashboardSummary = mcpgrafana.MustTool(
	"get_dashboard_summary",
	"Get a compact summary of a dashboard including title\\, panel count\\, panel types\\, variables\\, and other metadata without the full JSON. Use this for dashboard overview and planning modifications without consuming large context windows.",
	getDashboardSummary,
	mcp.WithTitleAnnotation("Get dashboard summary"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// Helper function to set a value at a JSONPath
func setValueAtPath(data map[string]interface{}, path string, value interface{}) error {
	return applyJSONPath(data, path, value, false)
}

// Helper function to remove a value at a JSONPath
func removeValueAtPath(data map[string]interface{}, path string) error {
	return applyJSONPath(data, path, nil, true)
}

// applyJSONPath applies a value to a JSONPath or removes it if remove=true
func applyJSONPath(data map[string]interface{}, path string, value interface{}, remove bool) error {
	// Remove the leading "$." if present
	if len(path) > 2 && path[:2] == "$." {
		path = path[2:]
	}

	// Split the path into segments
	segments := parseJSONPath(path)
	if len(segments) == 0 {
		return fmt.Errorf("empty JSONPath")
	}

	// Navigate to the parent of the target
	current := data
	for i, segment := range segments[:len(segments)-1] {
		next, err := navigateSegment(current, segment)
		if err != nil {
			return fmt.Errorf("at segment %d (%s): %w", i, segment.String(), err)
		}
		current = next
	}

	// Apply the final operation
	finalSegment := segments[len(segments)-1]
	if remove {
		return removeAtSegment(current, finalSegment)
	}
	return setAtSegment(current, finalSegment, value)
}

// JSONPathSegment represents a segment of a JSONPath
type JSONPathSegment struct {
	Key     string
	Index   int
	IsArray bool
}

func (s JSONPathSegment) String() string {
	if s.IsArray {
		return fmt.Sprintf("%s[%d]", s.Key, s.Index)
	}
	return s.Key
}

// parseJSONPath parses a JSONPath string into segments
func parseJSONPath(path string) []JSONPathSegment {
	var segments []JSONPathSegment

	// Simple parser for paths like "panels[0].targets[1].expr"
	i := 0
	for i < len(path) {
		// Find the key part
		keyStart := i
		for i < len(path) && path[i] != '[' && path[i] != '.' {
			i++
		}

		if keyStart == i {
			// Skip dots
			if i < len(path) && path[i] == '.' {
				i++
			}
			continue
		}

		key := path[keyStart:i]

		// Check if this is an array access
		if i < len(path) && path[i] == '[' {
			i++ // skip '['
			indexStart := i
			for i < len(path) && path[i] != ']' {
				i++
			}
			if i >= len(path) {
				break // malformed
			}

			indexStr := path[indexStart:i]
			index := 0
			if n, err := fmt.Sscanf(indexStr, "%d", &index); n == 1 && err == nil {
				segments = append(segments, JSONPathSegment{
					Key:     key,
					Index:   index,
					IsArray: true,
				})
			}
			i++ // skip ']'
		} else {
			segments = append(segments, JSONPathSegment{
				Key:     key,
				IsArray: false,
			})
		}

		// Skip dots
		if i < len(path) && path[i] == '.' {
			i++
		}
	}

	return segments
}

// navigateSegment navigates to the next level in the JSON structure
func navigateSegment(current map[string]interface{}, segment JSONPathSegment) (map[string]interface{}, error) {
	if segment.IsArray {
		// Get the array
		arr, ok := current[segment.Key].([]interface{})
		if !ok {
			return nil, fmt.Errorf("field '%s' is not an array", segment.Key)
		}

		if segment.Index < 0 || segment.Index >= len(arr) {
			return nil, fmt.Errorf("index %d out of bounds for array '%s' (length %d)", segment.Index, segment.Key, len(arr))
		}

		// Get the object at the index
		obj, ok := arr[segment.Index].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("element at %s[%d] is not an object", segment.Key, segment.Index)
		}

		return obj, nil
	} else {
		// Get the object
		obj, ok := current[segment.Key].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("field '%s' is not an object", segment.Key)
		}

		return obj, nil
	}
}

// setAtSegment sets a value at the final segment
func setAtSegment(current map[string]interface{}, segment JSONPathSegment, value interface{}) error {
	if segment.IsArray {
		// Get the array
		arr, ok := current[segment.Key].([]interface{})
		if !ok {
			return fmt.Errorf("field '%s' is not an array", segment.Key)
		}

		if segment.Index < 0 || segment.Index >= len(arr) {
			return fmt.Errorf("index %d out of bounds for array '%s' (length %d)", segment.Index, segment.Key, len(arr))
		}

		// Set the value in the array
		arr[segment.Index] = value
		return nil
	} else {
		// Set the value directly
		current[segment.Key] = value
		return nil
	}
}

// removeAtSegment removes a value at the final segment
func removeAtSegment(current map[string]interface{}, segment JSONPathSegment) error {
	if segment.IsArray {
		return fmt.Errorf("cannot remove array element %s[%d] (not supported)", segment.Key, segment.Index)
	} else {
		delete(current, segment.Key)
		return nil
	}
}

func AddDashboardTools(mcp *server.MCPServer) {
	GetDashboardByUID.Register(mcp)
	UpdateDashboard.Register(mcp)
	GetDashboardPanelQueries.Register(mcp)
	GetDashboardProperty.Register(mcp)
	GetDashboardSummary.Register(mcp)
}
