package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const (
	// DefaultCloudWatchLogsLimit is the default number of log rows to return
	DefaultCloudWatchLogsLimit = 100

	// MaxCloudWatchLogsLimit is the maximum number of log rows that can be requested
	MaxCloudWatchLogsLimit = 1000

	// cloudWatchLogsQueryTimeout bounds how long we poll for a Logs Insights query to complete
	cloudWatchLogsQueryTimeout = 30 * time.Second

	// cloudWatchLogsMaxPollInterval caps the exponential backoff between polls
	cloudWatchLogsMaxPollInterval = 2 * time.Second
)

// QueryCloudWatchLogsParams defines the parameters for a CloudWatch Logs Insights query
type QueryCloudWatchLogsParams struct {
	DatasourceUID string   `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource to query."`
	Region        string   `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
	LogGroupNames []string `json:"logGroupNames" jsonschema:"required,description=Log group names to query (e.g. [\"/aws/lambda/my-function\"])"`
	QueryString   string   `json:"queryString" jsonschema:"required,description=CloudWatch Logs Insights query (e.g. 'fields @timestamp\\, @message | filter @message like /ERROR/ | sort @timestamp desc | limit 20')"`
	Start         string   `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End           string   `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	Limit         int      `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return (default: 100\\, max: 1000). Applied to the result; a 'limit' clause in the query limits rows server-side."`
}

// CloudWatchLogsQueryResult represents the result of a CloudWatch Logs Insights query
type CloudWatchLogsQueryResult struct {
	// Logs holds one map per result row, keyed by field name (e.g. @timestamp, @message).
	Logs       []map[string]string `json:"logs"`
	TotalFound int                 `json:"totalFound"`
	Status     string              `json:"status"`
	Hints      []string            `json:"hints,omitempty"`
}

// ListCloudWatchLogGroupsParams defines the parameters for listing CloudWatch log groups
type ListCloudWatchLogGroupsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource"`
	Region        string `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
	Prefix        string `json:"prefix,omitempty" jsonschema:"description=Only return log groups whose name starts with this prefix"`
	AccountId     string `json:"accountId,omitempty" jsonschema:"description=AWS account ID for cross-account monitoring. Specify an account ID to filter log groups from a specific source account\\, or 'all' for all linked accounts."`
}

// ListCloudWatchLogGroupFieldsParams defines the parameters for listing fields in a log group
type ListCloudWatchLogGroupFieldsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource"`
	Region        string `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
	LogGroupName  string `json:"logGroupName" jsonschema:"required,description=The log group name to list fields for"`
	AccountId     string `json:"accountId,omitempty" jsonschema:"description=AWS account ID for cross-account monitoring."`
}

// cloudWatchNamedItem is the shape returned by the log-groups and
// log-group-fields resource APIs: [{"value": {"name": "...", ...}}]
type cloudWatchNamedItem struct {
	Value struct {
		Name string `json:"name"`
	} `json:"value"`
}

// parseCloudWatchNamedItems extracts value.name from a log-groups or log-group-fields response
func parseCloudWatchNamedItems(bodyBytes []byte) ([]string, error) {
	var items []cloudWatchNamedItem
	if err := json.Unmarshal(bodyBytes, &items); err != nil {
		return nil, err
	}

	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.Value.Name
	}
	return result, nil
}

// listCloudWatchLogGroups lists available CloudWatch log groups
func listCloudWatchLogGroups(ctx context.Context, args ListCloudWatchLogGroupsParams) ([]string, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	params := url.Values{}
	params.Set("region", args.Region)
	if args.Prefix != "" {
		params.Set("logGroupNamePrefix", args.Prefix)
	}
	if args.AccountId != "" {
		params.Set("accountId", args.AccountId)
	}

	bodyBytes, err := client.fetchResource(ctx, args.DatasourceUID, "log-groups", params)
	if err != nil {
		return nil, err
	}

	return parseCloudWatchNamedItems(bodyBytes)
}

// listCloudWatchLogGroupFields lists discovered fields for a CloudWatch log group
func listCloudWatchLogGroupFields(ctx context.Context, args ListCloudWatchLogGroupFieldsParams) ([]string, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	params := url.Values{}
	params.Set("region", args.Region)
	params.Set("logGroupName", args.LogGroupName)
	if args.AccountId != "" {
		params.Set("accountId", args.AccountId)
	}

	bodyBytes, err := client.fetchResource(ctx, args.DatasourceUID, "log-group-fields", params)
	if err != nil {
		return nil, err
	}

	return parseCloudWatchNamedItems(bodyBytes)
}

// queryCloudWatchLogs executes a CloudWatch Logs Insights query via Grafana.
// Logs Insights is asynchronous: StartQuery returns a query ID, and
// GetQueryResults is polled until the query completes.
func queryCloudWatchLogs(ctx context.Context, args QueryCloudWatchLogsParams) (*CloudWatchLogsQueryResult, error) {
	if len(args.LogGroupNames) == 0 {
		return nil, errors.New("at least one log group name is required")
	}

	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	now := time.Now()
	fromTime := now.Add(-1 * time.Hour)
	toTime := now

	if args.Start != "" {
		parsed, err := parseStartTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}
	}

	if args.End != "" {
		parsed, err := parseEndTime(args.End)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}
	}

	frames, err := client.runLogsQuery(ctx, args, fromTime, toTime, cloudWatchLogsQueryTimeout)
	if err != nil {
		return nil, err
	}

	limit := args.Limit
	if limit <= 0 {
		limit = DefaultCloudWatchLogsLimit
	}
	if limit > MaxCloudWatchLogsLimit {
		limit = MaxCloudWatchLogsLimit
	}

	return parseLogsFrames(frames, limit), nil
}

// runLogsQuery starts a Logs Insights query and polls for its results with
// exponential backoff until it completes, fails, or timeout elapses.
func (c *cloudWatchClient) runLogsQuery(ctx context.Context, args QueryCloudWatchLogsParams, from, to time.Time, timeout time.Duration) (data.Frames, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	query := map[string]interface{}{
		"datasource": map[string]string{
			"uid":  args.DatasourceUID,
			"type": CloudWatchDatasourceType,
		},
		"refId":         "A",
		"type":          "logAction",
		"subtype":       "StartQuery",
		"queryMode":     "Logs",
		"region":        args.Region,
		"queryString":   args.QueryString,
		"logGroupNames": args.LogGroupNames,
		"id":            "",
		"intervalMs":    1,
		"maxDataPoints": 1,
	}

	resp, err := doDSQuery(ctx, c.httpClient, c.baseURL, dsQueryPayload(from, to, query))
	if err != nil {
		return nil, fmt.Errorf("starting CloudWatch Logs query: %w", err)
	}
	frames, err := logsQueryFrames(resp)
	if err != nil {
		return nil, fmt.Errorf("starting CloudWatch Logs query: %w", err)
	}
	queryID := logsQueryID(frames)
	if queryID == "" {
		return nil, errors.New("no queryId found in StartQuery response")
	}

	query["subtype"] = "GetQueryResults"
	query["queryId"] = queryID
	delete(query, "queryString")
	delete(query, "logGroupNames")
	delete(query, "maxDataPoints")

	interval := 200 * time.Millisecond
	for {
		resp, err := doDSQuery(ctx, c.httpClient, c.baseURL, dsQueryPayload(from, to, query))
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("CloudWatch Logs query timed out after %s", timeout)
			}
			return nil, fmt.Errorf("polling CloudWatch Logs query results: %w", err)
		}
		frames, err := logsQueryFrames(resp)
		if err != nil {
			return nil, err
		}

		switch status := logsQueryStatus(frames); status {
		case "Complete":
			return frames, nil
		case "Running", "Scheduled", "":
		default:
			return nil, fmt.Errorf("CloudWatch Logs query finished with status %s", status)
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("CloudWatch Logs query timed out after %s", timeout)
			}
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		interval = min(interval*2, cloudWatchLogsMaxPollInterval)
	}
}

// logsQueryFrames returns the frames for refId A, or that query's error.
func logsQueryFrames(resp *backend.QueryDataResponse) (data.Frames, error) {
	r, ok := resp.Responses["A"]
	if !ok {
		return nil, errors.New("no result for query refId A")
	}
	if r.Error != nil {
		return nil, fmt.Errorf("query error: %w", r.Error)
	}
	return r.Frames, nil
}

// logsQueryID finds the queryId field the CloudWatch plugin returns from StartQuery.
func logsQueryID(frames data.Frames) string {
	for _, frame := range frames {
		for _, field := range frame.Fields {
			if field.Name == "queryId" && field.Len() > 0 {
				if v, ok := field.ConcreteAt(0); ok {
					if s, ok := v.(string); ok {
						return s
					}
				}
			}
		}
	}
	return ""
}

// logsQueryStatus reads the query status from frame metadata, which the
// CloudWatch plugin sets as {"Status": "Complete"} on GetQueryResults frames.
func logsQueryStatus(frames data.Frames) string {
	for _, frame := range frames {
		if frame.Meta == nil {
			continue
		}
		if custom, ok := frame.Meta.Custom.(map[string]interface{}); ok {
			if s, ok := custom["Status"].(string); ok {
				return s
			}
		}
	}
	return ""
}

// parseLogsFrames converts GetQueryResults frames into rows, keeping at most
// limit rows and dropping Grafana-internal fields such as @ptr.
func parseLogsFrames(frames data.Frames, limit int) *CloudWatchLogsQueryResult {
	result := &CloudWatchLogsQueryResult{
		Logs:   []map[string]string{},
		Status: logsQueryStatus(frames),
	}

	for _, frame := range frames {
		rowCount := frame.Rows()
		result.TotalFound += rowCount
		for row := 0; row < rowCount && len(result.Logs) < limit; row++ {
			entry := make(map[string]string, len(frame.Fields))
			for _, field := range frame.Fields {
				if field.Name == "@ptr" || strings.HasSuffix(field.Name, "__grafana_internal__") {
					continue
				}
				entry[field.Name] = formatLogValue(field, row)
			}
			result.Logs = append(result.Logs, entry)
		}
	}

	if result.TotalFound == 0 {
		result.Hints = generateCloudWatchLogsEmptyResultHints()
	}

	return result
}

// formatLogValue renders a field value as a string, with times as RFC3339 UTC.
func formatLogValue(field *data.Field, row int) string {
	v, ok := field.ConcreteAt(row)
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case time.Time:
		return val.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(val)
	}
}

// generateCloudWatchLogsEmptyResultHints generates helpful hints when a Logs Insights query returns no rows
func generateCloudWatchLogsEmptyResultHints() []string {
	return []string{
		"No log data found. Possible reasons:",
		"- Log group name may be incorrect - use list_cloudwatch_log_groups to discover available groups",
		"- Field names may be wrong - use list_cloudwatch_log_group_fields to see the fields in a log group",
		"- Filter may be too restrictive - try a broader filter or remove it",
		"- Region may be incorrect - verify the log group exists in the specified region",
		"- Time range may have no log events - try extending with start=\"now-6h\"",
	}
}

// QueryCloudWatchLogs is a tool for executing CloudWatch Logs Insights queries via Grafana
var QueryCloudWatchLogs = mcpgrafana.MustTool(
	"query_cloudwatch_logs",
	`Run a CloudWatch Logs Insights query against one or more log groups via Grafana and return the result rows. Requires region. Waits up to 30s for the query to complete.

Query examples:
- fields @timestamp, @message | sort @timestamp desc | limit 20
- filter @message like /error/i | stats count() by bin(5m)

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)`,
	queryCloudWatchLogs,
	mcpgrafana.WithTitleAnnotation("Query CloudWatch Logs"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// ListCloudWatchLogGroups is a tool for listing CloudWatch log groups
var ListCloudWatchLogGroups = mcpgrafana.MustTool(
	"list_cloudwatch_log_groups",
	"List CloudWatch log group names, optionally filtered by name prefix. Requires region. Supports cross-account monitoring via optional accountId parameter.",
	listCloudWatchLogGroups,
	mcpgrafana.WithTitleAnnotation("List CloudWatch log groups"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// ListCloudWatchLogGroupFields is a tool for listing fields in a CloudWatch log group
var ListCloudWatchLogGroupFields = mcpgrafana.MustTool(
	"list_cloudwatch_log_group_fields",
	"List the fields discovered in a CloudWatch log group, for use in Logs Insights queries. Requires region.",
	listCloudWatchLogGroupFields,
	mcpgrafana.WithTitleAnnotation("List CloudWatch log group fields"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)
