package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/PaesslerAG/gval"
	"github.com/PaesslerAG/jsonpath"
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

// PatchOperation represents a single patch operation
type PatchOperation struct {
	Op    string      `json:"op" jsonschema:"required,description=Operation type: 'replace'\\, 'add'\\, 'remove'"`
	Path  string      `json:"path" jsonschema:"required,description=JSONPath to the property to modify. Supports: '$.title'\\, '$.panels[0].title'\\, '$.panels[0].targets[0].expr'\\, '$.panels[1].targets[0].datasource'\\, etc. For appending to arrays\\, use '/- ' syntax: '$.panels/- ' (append to panels array) or '$.panels[2]/- ' (append to nested array at index 2)."`
	Value interface{} `json:"value,omitempty" jsonschema:"description=New value for replace/add operations"`
}

type UpdateDashboardParams struct {
	// For full dashboard updates (creates new dashboards or complete rewrites)
	Dashboard map[string]interface{} `json:"dashboard,omitempty" jsonschema:"description=The full dashboard JSON. Use for creating new dashboards or complete updates. Large dashboards consume significant context - consider using patches for small changes."`

	// For targeted updates using patch operations (preferred for existing dashboards)
	UID        string           `json:"uid,omitempty" jsonschema:"description=UID of existing dashboard to update. Must be used together with 'operations'. Providing 'uid' without 'operations' will fail."`
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
	} else if args.UID != "" && len(args.Operations) == 0 {
		return nil, fmt.Errorf("'uid' was provided without 'operations'. To update an existing dashboard, provide both 'uid' and 'operations' (array of patch operations). To replace a dashboard entirely, provide 'dashboard' (full JSON) instead")
	} else if len(args.Operations) > 0 && args.UID == "" {
		return nil, fmt.Errorf("'operations' were provided without 'uid'. To apply patch operations, provide the 'uid' of the existing dashboard to update along with the 'operations' array")
	} else {
		return nil, fmt.Errorf("no dashboard content provided. You must use one of two modes: (1) Patch mode (preferred for existing dashboards): provide 'uid' + 'operations' array with targeted changes. (2) Full JSON mode: provide 'dashboard' with the complete dashboard object. Do NOT retry this same call — choose a mode and provide the required fields")
	}
}

// updateDashboardWithPatches applies patch operations to an existing dashboard
func updateDashboardWithPatches(ctx context.Context, args UpdateDashboardParams) (*models.PostDashboardOKBody, error) {
	// Sort array element remove operations from highest to lowest index to avoid index-shifting issues
	sortedOps, err := sortArrayRemovesDescending(args.Operations)
	if err != nil {
		return nil, err
	}
	args.Operations = sortedOps

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
			if err := applyJSONPath(dashboardMap, op.Path, op.Value, false); err != nil {
				return nil, fmt.Errorf("operation %d (%s at %s): %w", i, op.Op, op.Path, err)
			}
		case "remove":
			if err := applyJSONPath(dashboardMap, op.Path, nil, true); err != nil {
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
		Overwrite: true,
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

// sortArrayRemovesDescending reorders remove operations on the same array
// from highest index to lowest. This prevents the index-shifting footgun
// where removing a lower index first causes subsequent operations to target wrong elements.
// It also rejects duplicate indices on the same array (likely an LLM mistake).
func sortArrayRemovesDescending(operations []PatchOperation) ([]PatchOperation, error) {
	type arrayRemoveInfo struct {
		arrayPath string
		index     int
		opIndex   int
	}

	// Collect array remove operations grouped by array path
	removesByArray := make(map[string][]arrayRemoveInfo)

	for i, op := range operations {
		if op.Op != "remove" {
			continue
		}

		// Parse the path to check if it's an array element removal
		path := op.Path
		if len(path) > 2 && path[:2] == "$." {
			path = path[2:]
		}

		segments := parseJSONPath(path)
		if len(segments) == 0 {
			continue
		}

		// Check if the final segment is an array access
		finalSeg := segments[len(segments)-1]
		if !finalSeg.IsArray || finalSeg.IsAppend {
			continue
		}

		// Build the array path (everything except the index)
		arrayPath := ""
		for j, seg := range segments {
			if j > 0 {
				arrayPath += "."
			}
			arrayPath += seg.Key
			if seg.IsArray && j < len(segments)-1 {
				arrayPath += fmt.Sprintf("[%d]", seg.Index)
			}
		}

		removesByArray[arrayPath] = append(removesByArray[arrayPath], arrayRemoveInfo{
			arrayPath: arrayPath,
			index:     finalSeg.Index,
			opIndex:   i,
		})
	}

	// Check for duplicate indices and sort each group descending
	for arrayPath, removes := range removesByArray {
		// Check for duplicate indices
		seen := make(map[int]bool)
		for _, r := range removes {
			if seen[r.index] {
				return nil, fmt.Errorf("duplicate remove at index %d on '%s'; each index should only be removed once", r.index, arrayPath)
			}
			seen[r.index] = true
		}

		// Sort descending by index
		sort.Slice(removes, func(i, j int) bool {
			return removes[i].index > removes[j].index
		})
		removesByArray[arrayPath] = removes
	}

	// Rebuild the operations slice with array removes reordered
	result := make([]PatchOperation, len(operations))
	copy(result, operations)

	for _, removes := range removesByArray {
		if len(removes) <= 1 {
			continue
		}
		// Collect the original positions of these remove ops
		positions := make([]int, len(removes))
		for i, r := range removes {
			positions[i] = r.opIndex
		}
		// Sort positions ascending so we can place sorted removes in order
		sort.Ints(positions)
		// Place the sorted (descending by index) removes into the original positions
		for i, pos := range positions {
			result[pos] = operations[removes[i].opIndex]
		}
	}

	return result, nil
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
	"Create or update a dashboard. Two modes: (1) Full JSON — provide 'dashboard' for new dashboards or complete replacements. (2) Patch — provide 'uid' + 'operations' to make targeted changes to an existing dashboard. One of these two modes is required; 'folderUid'\\, 'message'\\, and 'overwrite' are supplementary and do nothing on their own. Patch operations support JSONPaths like '$.panels[0].targets[0].expr'\\, '$.panels[1].title'\\, '$.panels[2].targets[0].datasource'. Append to arrays with '/- ' syntax: '$.panels/- '. Remove by index: {\"op\": \"remove\"\\, \"path\": \"$.panels[2]\"}. Multiple removes on the same array are automatically reordered to avoid index-shifting issues.",
	updateDashboard,
	mcp.WithTitleAnnotation("Create or update dashboard"),
	mcp.WithDestructiveHintAnnotation(true),
)

type DashboardPanelQueriesParams struct {
	UID       string            `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
	PanelID   *int              `json:"panelId,omitempty" jsonschema:"description=Optional panel ID to filter to a specific panel"`
	Variables map[string]string `json:"variables,omitempty" jsonschema:"description=Optional variable substitutions (e.g.\\, {\"job\": \"api-server\"})"`
}

type datasourceInfo struct {
	UID  string `json:"uid"`
	Type string `json:"type"`
}

type panelQuery struct {
	Title             string         `json:"title"`
	Query             string         `json:"query"`
	ProcessedQuery    string         `json:"processedQuery,omitempty"`
	Datasource        datasourceInfo `json:"datasource"`
	RefID             string         `json:"refId,omitempty"`
	RequiredVariables []VariableInfo `json:"requiredVariables,omitempty"`
}

func GetDashboardPanelQueriesTool(ctx context.Context, args DashboardPanelQueriesParams) ([]panelQuery, error) {
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams{UID: args.UID})
	if err != nil {
		return nil, fmt.Errorf("get dashboard by uid: %w", err)
	}

	db, ok := dashboard.Dashboard.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	// Determine if variable processing is needed
	var dashboardVars map[string]VariableInfo
	if args.Variables != nil {
		dashboardVars = extractDashboardVariables(db)
	}

	// Determine which panels to process
	var panels []map[string]interface{}
	if args.PanelID != nil {
		panel, err := findPanelByID(db, *args.PanelID)
		if err != nil {
			return nil, err
		}
		panels = []map[string]interface{}{panel}
	} else {
		panels = collectAllPanels(db)
	}

	var result []panelQuery
	for _, panel := range panels {
		queries := extractPanelQueries(panel, dashboardVars, args.Variables)
		result = append(result, queries...)
	}

	return result, nil
}

var GetDashboardPanelQueries = mcpgrafana.MustTool(
	"get_dashboard_panel_queries",
	"Retrieve panel queries from a Grafana dashboard. Supports all datasource types (Prometheus, Loki, CloudWatch, SQL, etc.) and row-nested panels. Optionally filter to a specific panel by ID with `panelId`. Optionally provide `variables` for template variable substitution, which populates `processedQuery` and `requiredVariables` fields. Returns an array of objects with fields: title, query (raw expression), datasource (object with uid and type), and optionally processedQuery, refId, and requiredVariables.",
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
	builder := gval.Full(jsonpath.Language())
	path, err := builder.NewEvaluable(args.JSONPath)
	if err != nil {
		return nil, fmt.Errorf("create JSONPath evaluable '%s': %w", args.JSONPath, err)
	}

	result, err := path(ctx, dashboardData)
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
	dashboard, err := getDashboardByUID(ctx, GetDashboardByUIDParams(args))
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

	// Extract basic info using helper functions
	extractBasicDashboardInfo(db, summary)

	// Extract time range
	summary.TimeRange = extractTimeRange(db)

	// Extract panel summaries
	if panels := safeArray(db, "panels"); panels != nil {
		summary.PanelCount = len(panels)
		for _, p := range panels {
			if panelObj, ok := p.(map[string]interface{}); ok {
				summary.Panels = append(summary.Panels, extractPanelSummary(panelObj))
			}
		}
	}

	// Extract variable summaries
	if templating := safeObject(db, "templating"); templating != nil {
		if list := safeArray(templating, "list"); list != nil {
			for _, v := range list {
				if variable, ok := v.(map[string]interface{}); ok {
					summary.Variables = append(summary.Variables, extractVariableSummary(variable))
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

// applyJSONPath applies a value to a JSONPath or removes it if remove=true
func applyJSONPath(data map[string]interface{}, path string, value interface{}, remove bool) error {
	// Trim whitespace to handle paths like "$.panels/- " (trailing space)
	path = strings.TrimSpace(path)

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
	Key      string
	Index    int
	IsArray  bool
	IsAppend bool // true when using /- syntax to append to array
}

func (s JSONPathSegment) String() string {
	if s.IsAppend {
		return fmt.Sprintf("%s/-", s.Key)
	}
	if s.IsArray {
		return fmt.Sprintf("%s[%d]", s.Key, s.Index)
	}
	return s.Key
}

// parseJSONPath parses a JSONPath string into segments
// Supports paths like "panels[0].targets[1].expr", "title", "templating.list[0].name"
// Also supports append syntax: "panels/-" or "panels[2]/-"
func parseJSONPath(path string) []JSONPathSegment {
	var segments []JSONPathSegment

	// Handle empty path
	if path == "" {
		return segments
	}

	// Enhanced regex to handle /- append syntax
	// Matches: key, key[index], key/-, key[index]/-
	re := regexp.MustCompile(`([^.\[\]\/]+)(?:\[(\d+)\])?(?:(\/-))?`)
	matches := re.FindAllStringSubmatch(path, -1)

	for _, match := range matches {
		if len(match) >= 2 && match[1] != "" {
			segment := JSONPathSegment{
				Key:      match[1],
				IsArray:  len(match) >= 3 && match[2] != "",
				IsAppend: len(match) >= 4 && match[3] == "/-",
			}

			if segment.IsArray && !segment.IsAppend {
				if index, err := strconv.Atoi(match[2]); err == nil {
					segment.Index = index
				}
			}

			segments = append(segments, segment)
		}
	}

	return segments
}

// validateArrayAccess validates array access for a segment
func validateArrayAccess(current map[string]interface{}, segment JSONPathSegment) ([]interface{}, error) {
	arr, ok := current[segment.Key].([]interface{})
	if !ok {
		return nil, fmt.Errorf("field '%s' is not an array", segment.Key)
	}

	// For append operations, we don't need to validate index bounds
	if segment.IsAppend {
		return arr, nil
	}

	if segment.Index < 0 || segment.Index >= len(arr) {
		return nil, fmt.Errorf("index %d out of bounds for array '%s' (length %d)", segment.Index, segment.Key, len(arr))
	}

	return arr, nil
}

// navigateSegment navigates to the next level in the JSON structure
func navigateSegment(current map[string]interface{}, segment JSONPathSegment) (map[string]interface{}, error) {
	// Append operations can only be at the final segment
	if segment.IsAppend {
		return nil, fmt.Errorf("append operation (/- ) can only be used at the final path segment")
	}

	if segment.IsArray {
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return nil, err
		}

		// Get the object at the index
		obj, ok := arr[segment.Index].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("element at %s[%d] is not an object", segment.Key, segment.Index)
		}

		return obj, nil
	}

	// Get the object
	obj, ok := current[segment.Key].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("field '%s' is not an object", segment.Key)
	}

	return obj, nil
}

// setAtSegment sets a value at the final segment
func setAtSegment(current map[string]interface{}, segment JSONPathSegment, value interface{}) error {
	if segment.IsAppend {
		// Handle append operation: add to the end of the array
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return err
		}

		// Append the value to the array
		arr = append(arr, value)
		current[segment.Key] = arr
		return nil
	}

	if segment.IsArray {
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return err
		}

		// Set the value in the array
		arr[segment.Index] = value
		return nil
	}

	// Set the value directly
	current[segment.Key] = value
	return nil
}

// removeAtSegment removes a value at the final segment
func removeAtSegment(current map[string]interface{}, segment JSONPathSegment) error {
	if segment.IsAppend {
		return fmt.Errorf("cannot use remove operation with append syntax (/- ) at %s", segment.Key)
	}

	if segment.IsArray {
		arr, err := validateArrayAccess(current, segment)
		if err != nil {
			return err
		}
		current[segment.Key] = slices.Delete(arr, segment.Index, segment.Index+1)
		return nil
	}

	delete(current, segment.Key)
	return nil
}

// Helper functions for safe type conversions and field extraction

// safeGet safely extracts a value from a map with type conversion
func safeGet[T any](data map[string]interface{}, key string, defaultVal T) T {
	if val, ok := data[key]; ok {
		if typedVal, ok := val.(T); ok {
			return typedVal
		}
	}
	return defaultVal
}

func safeString(data map[string]interface{}, key string) string {
	return safeGet(data, key, "")
}

func safeStringSlice(data map[string]interface{}, key string) []string {
	var result []string
	if arr := safeArray(data, key); arr != nil {
		for _, item := range arr {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
	}
	return result
}

func safeFloat64(data map[string]interface{}, key string) float64 {
	return safeGet(data, key, 0.0)
}

func safeInt(data map[string]interface{}, key string) int {
	return int(safeFloat64(data, key))
}

func safeObject(data map[string]interface{}, key string) map[string]interface{} {
	return safeGet(data, key, map[string]interface{}(nil))
}

func safeArray(data map[string]interface{}, key string) []interface{} {
	return safeGet(data, key, []interface{}(nil))
}

// extractBasicDashboardInfo extracts common dashboard fields
func extractBasicDashboardInfo(db map[string]interface{}, summary *DashboardSummary) {
	summary.Title = safeString(db, "title")
	summary.Description = safeString(db, "description")
	summary.Tags = safeStringSlice(db, "tags")
	summary.Refresh = safeString(db, "refresh")
}

// extractTimeRange extracts time range information
func extractTimeRange(db map[string]interface{}) TimeRangeSummary {
	timeObj := safeObject(db, "time")
	if timeObj == nil {
		return TimeRangeSummary{}
	}

	return TimeRangeSummary{
		From: safeString(timeObj, "from"),
		To:   safeString(timeObj, "to"),
	}
}

// extractPanelSummary creates a panel summary from panel data
func extractPanelSummary(panel map[string]interface{}) PanelSummary {
	summary := PanelSummary{
		ID:          safeInt(panel, "id"),
		Title:       safeString(panel, "title"),
		Type:        safeString(panel, "type"),
		Description: safeString(panel, "description"),
	}

	// Count queries
	if targets := safeArray(panel, "targets"); targets != nil {
		summary.QueryCount = len(targets)
	}

	return summary
}

// extractVariableSummary creates a variable summary from variable data
func extractVariableSummary(variable map[string]interface{}) VariableSummary {
	return VariableSummary{
		Name:  safeString(variable, "name"),
		Type:  safeString(variable, "type"),
		Label: safeString(variable, "label"),
	}
}

func AddDashboardTools(mcp *server.MCPServer, enableWriteTools bool) {
	GetDashboardByUID.Register(mcp)
	if enableWriteTools {
		UpdateDashboard.Register(mcp)
	}
	GetDashboardPanelQueries.Register(mcp)
	GetDashboardProperty.Register(mcp)
	GetDashboardSummary.Register(mcp)
}
