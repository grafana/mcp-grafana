package tools

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/internal/datasource"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	AzureMonitorDatasourceType = "grafana-azure-monitor-datasource"

	DefaultAzureMonitorLimit = 100

	MaxAzureLogAnalyticsLimit = 1000

	// Resource Graph returns at most 1000 rows per request, and the tool asks for limit + 1.
	MaxAzureResourceGraphLimit = 999

	DefaultAzureLogAnalyticsTimeColumn = "TimeGenerated"

	azureLogAnalyticsQueryType  = "Azure Log Analytics"
	azureResourceGraphQueryType = "Azure Resource Graph"
)

// newAzureMonitorClient validates the datasource type and returns an /api/ds/query client.
func newAzureMonitorClient(ctx context.Context, uid string) (*http.Client, string, error) {
	ds, err := datasource.ByUID(ctx, uid)
	if err != nil {
		return nil, "", err
	}
	if ds.Type != AzureMonitorDatasourceType {
		return nil, "", fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, AzureMonitorDatasourceType)
	}
	return newDSQueryHTTPClient(ctx)
}

func normalizeAzureMonitorLimit(limit, maxLimit int) int {
	if limit <= 0 {
		return DefaultAzureMonitorLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// render must be the last operator, so the appended take would break it.
var kqlRenderOperator = regexp.MustCompile(`(?i)\|\s*render\b`)

// validateAzureMonitorKQL rejects queries the tool can't run as given.
func validateAzureMonitorKQL(query string) error {
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("query is required")
	}
	if kqlRenderOperator.MatchString(query) {
		return fmt.Errorf("the render operator is not supported: remove the '| render ...' clause, the tool returns rows")
	}
	return nil
}

// kqlWithTake caps the rows a KQL query returns. The take goes on its own line so a trailing // comment can't swallow it.
func kqlWithTake(query string, n int) string {
	q := strings.TrimRight(strings.TrimSpace(query), "; \t\r\n")
	return fmt.Sprintf("%s\n| take %d", q, n)
}

// AzureMonitorQueryResult is the tabular result of an Azure Monitor KQL query.
type AzureMonitorQueryResult struct {
	Columns   []string                 `json:"columns"`
	Rows      []map[string]interface{} `json:"rows"`
	RowCount  int                      `json:"rowCount"`
	Limit     int                      `json:"limit"`
	Truncated bool                     `json:"truncated"`
	Hints     *EmptyResultHints        `json:"hints,omitempty"`
}

func runAzureMonitorQuery(ctx context.Context, datasourceUID string, query map[string]interface{}, from, to time.Time, limit int) (*AzureMonitorQueryResult, error) {
	client, baseURL, err := newAzureMonitorClient(ctx, datasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Azure Monitor client: %w", err)
	}

	resp, err := doDSQuery(ctx, client, baseURL, dsQueryPayload(from, to, query))
	if err != nil {
		return nil, err
	}

	columns, rows, err := framesToTabularRows(resp)
	if err != nil {
		return nil, err
	}

	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	return &AzureMonitorQueryResult{
		Columns:   columns,
		Rows:      rows,
		RowCount:  len(rows),
		Limit:     limit,
		Truncated: truncated,
	}, nil
}

// AzureLogAnalyticsQueryParams defines the parameters for query_azure_log_analytics.
type AzureLogAnalyticsQueryParams struct {
	DatasourceUID string   `json:"datasourceUid" jsonschema:"required,description=The UID of the Azure Monitor datasource (type grafana-azure-monitor-datasource)."`
	Query         string   `json:"query" jsonschema:"required,description=KQL query\\, e.g. ContainerAppConsoleLogs_CL | where Log_s contains \"error\" | project TimeGenerated\\, ContainerAppName_s\\, Log_s. Do NOT add a time filter: start/end are applied automatically. Do NOT add take/limit (the tool appends one from 'limit') or render."`
	Resources     []string `json:"resources" jsonschema:"required,description=Azure resource IDs to query: Log Analytics workspaces (/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.OperationalInsights/workspaces/<name>) or resources with diagnostic logs. Several workspaces are queried together."`
	Start         string   `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End           string   `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	TimeColumn    string   `json:"timeColumn,omitempty" jsonschema:"description=Column the start/end range is applied to. Default: TimeGenerated"`
	Limit         int      `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return. Default: 100\\, max: 1000."`
}

func buildAzureLogAnalyticsQuery(args AzureLogAnalyticsQueryParams, limit int) map[string]interface{} {
	timeColumn := args.TimeColumn
	if timeColumn == "" {
		timeColumn = DefaultAzureLogAnalyticsTimeColumn
	}
	return map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"uid":  args.DatasourceUID,
			"type": AzureMonitorDatasourceType,
		},
		"queryType": azureLogAnalyticsQueryType,
		"azureLogAnalytics": map[string]interface{}{
			"query":         kqlWithTake(args.Query, limit+1),
			"resources":     args.Resources,
			"resultFormat":  "table",
			"dashboardTime": true,
			"timeColumn":    timeColumn,
		},
	}
}

func parseAzureLogAnalyticsTimeRange(start, end string) (time.Time, time.Time, error) {
	now := time.Now()
	from, to := now.Add(-time.Hour), now
	if start != "" {
		parsed, err := parseStartTime(start)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			from = parsed
		}
	}
	if end != "" {
		parsed, err := parseEndTime(end)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			to = parsed
		}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("end time must be after start time")
	}
	return from, to, nil
}

func queryAzureLogAnalytics(ctx context.Context, args AzureLogAnalyticsQueryParams) (*AzureMonitorQueryResult, error) {
	if err := validateAzureMonitorKQL(args.Query); err != nil {
		return nil, err
	}
	if len(args.Resources) == 0 || slices.ContainsFunc(args.Resources, func(r string) bool { return strings.TrimSpace(r) == "" }) {
		return nil, fmt.Errorf("resources is required: pass at least one Log Analytics workspace or resource ID, with no empty entries")
	}
	from, to, err := parseAzureLogAnalyticsTimeRange(args.Start, args.End)
	if err != nil {
		return nil, err
	}

	limit := normalizeAzureMonitorLimit(args.Limit, MaxAzureLogAnalyticsLimit)
	result, err := runAzureMonitorQuery(ctx, args.DatasourceUID, buildAzureLogAnalyticsQuery(args, limit), from, to, limit)
	if err != nil {
		return nil, err
	}

	if result.RowCount == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "azureloganalytics",
			Query:          args.Query,
			StartTime:      from,
			EndTime:        to,
		})
	}
	return result, nil
}

var QueryAzureLogAnalytics = mcpgrafana.MustTool(
	"query_azure_log_analytics",
	`Run a KQL query against Azure Log Analytics workspaces through an Azure Monitor datasource. Returns column names and rows.

The time range (start/end, default last hour) is applied to timeColumn (default TimeGenerated), so the query needs no time filter. Results are capped at 'limit' (default 100, max 1000) by appending '| take'; 'truncated' is true when more rows matched.

Query examples:
  ContainerAppConsoleLogs_CL | where Log_s has "Exception" | project TimeGenerated, ContainerAppName_s, Log_s | order by TimeGenerated desc
  AppRequests | where Success == false | summarize failures = count() by Name, ResultCode
  AzureDiagnostics | where Category == "FunctionAppLogs" and Level == "Error"
  search "timeout" | summarize count() by $table`,
	queryAzureLogAnalytics,
	mcpgrafana.WithTitleAnnotation("Query Azure Log Analytics"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// AzureResourceGraphQueryParams defines the parameters for query_azure_resource_graph.
type AzureResourceGraphQueryParams struct {
	DatasourceUID string   `json:"datasourceUid" jsonschema:"required,description=The UID of the Azure Monitor datasource (type grafana-azure-monitor-datasource)."`
	Query         string   `json:"query" jsonschema:"required,description=Resource Graph KQL query\\, e.g. resources | where type == \"microsoft.app/containerapps\" | project name\\, resourceGroup\\, properties.runningStatus. Do NOT add take/limit (the tool appends one from 'limit') or render."`
	Subscriptions []string `json:"subscriptions,omitempty" jsonschema:"description=Subscription IDs to query. Omit to query every subscription the datasource's credentials can read."`
	Limit         int      `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return. Default: 100\\, max: 999."`
}

func buildAzureResourceGraphQuery(args AzureResourceGraphQueryParams, limit int) map[string]interface{} {
	q := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"uid":  args.DatasourceUID,
			"type": AzureMonitorDatasourceType,
		},
		"queryType": azureResourceGraphQueryType,
		"azureResourceGraph": map[string]interface{}{
			"query":        kqlWithTake(args.Query, limit+1),
			"resultFormat": "table",
		},
	}
	if len(args.Subscriptions) > 0 {
		q["subscriptions"] = args.Subscriptions
	}
	return q
}

func queryAzureResourceGraph(ctx context.Context, args AzureResourceGraphQueryParams) (*AzureMonitorQueryResult, error) {
	if err := validateAzureMonitorKQL(args.Query); err != nil {
		return nil, err
	}

	limit := normalizeAzureMonitorLimit(args.Limit, MaxAzureResourceGraphLimit)
	now := time.Now()
	return runAzureMonitorQuery(ctx, args.DatasourceUID, buildAzureResourceGraphQuery(args, limit), now.Add(-time.Hour), now, limit)
}

var QueryAzureResourceGraph = mcpgrafana.MustTool(
	"query_azure_resource_graph",
	`Run an Azure Resource Graph KQL query through an Azure Monitor datasource to inventory Azure resources and their current state. Returns column names and rows. Resource Graph has no time range: it reflects the current state.

Results are capped at 'limit' (default 100, max 999) by appending '| take'; 'truncated' is true when more rows matched.

Query examples:
  resources | where type == "microsoft.app/containerapps" | project name, resourceGroup, status = properties.runningStatus
  resources | where type == "microsoft.operationalinsights/workspaces" | project id, name, resourceGroup
  resources | summarize count() by type | order by count_ desc
  resourcecontainers | where type == "microsoft.resources/subscriptions" | project subscriptionId, name`,
	queryAzureResourceGraph,
	mcpgrafana.WithTitleAnnotation("Query Azure Resource Graph"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// AddAzureMonitorTools registers the Azure Monitor tools with the MCP server.
func AddAzureMonitorTools(s *mcp.Server, enableQueryTools bool) {
	if enableQueryTools {
		QueryAzureLogAnalytics.Register(s)
		QueryAzureResourceGraph.Register(s)
	}
}
