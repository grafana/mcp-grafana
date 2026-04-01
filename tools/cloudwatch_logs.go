package tools

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	// DefaultCloudWatchLogsLimit is the default number of log entries to return
	DefaultCloudWatchLogsLimit = 100

	// MaxCloudWatchLogsLimit is the maximum number of log entries that can be requested
	MaxCloudWatchLogsLimit = 1000

	// defaultLogsQueryTimeout is the maximum time to wait for a Logs Insights query to complete
	defaultLogsQueryTimeout = 30 * time.Second

	// initialPollInterval is the starting interval for polling GetQueryResults
	initialPollInterval = 200 * time.Millisecond

	// maxPollInterval is the maximum interval between polling attempts
	maxPollInterval = 2 * time.Second

	// pollBackoffMultiplier is the multiplier for exponential backoff
	pollBackoffMultiplier = 1.5
)

// QueryCloudWatchLogsParams defines the parameters for a CloudWatch Logs Insights query
type QueryCloudWatchLogsParams struct {
	DatasourceUID string   `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource. Use list_datasources to find available UIDs."`
	Region        string   `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
	LogGroupNames []string `json:"logGroupNames" jsonschema:"required,description=List of log group names to query (e.g. [\"cloudwatch-prod\"\\, \"/aws/lambda/my-function\"]). Use list_cloudwatch_log_groups to discover available groups."`
	QueryString   string   `json:"queryString" jsonschema:"required,description=CloudWatch Logs Insights query string. Example: 'fields @timestamp\\, @message | filter @message like /ERROR/ | sort @timestamp desc | limit 20'"`
	Start         string   `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End           string   `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	Limit         int      `json:"limit,omitempty" jsonschema:"description=Maximum number of log entries to return (default: 100\\, max: 1000). Note: this is applied to the result; include a 'limit' clause in your query for server-side limiting."`
}

// CloudWatchLogsQueryResult represents the result of a CloudWatch Logs Insights query
type CloudWatchLogsQueryResult struct {
	Logs       []CloudWatchLogEntry `json:"logs"`
	Query      string               `json:"query"`
	TotalFound int                  `json:"totalFound"`
	Status     string               `json:"status"`
	Hints      []string             `json:"hints,omitempty"`
}

// CloudWatchLogEntry represents a single log entry returned by Logs Insights.
// Fields are dynamic based on the query (e.g. @timestamp, @message, custom fields).
type CloudWatchLogEntry struct {
	Fields map[string]string `json:"fields"`
}

// ListCloudWatchLogGroupsParams defines the parameters for listing CloudWatch log groups
type ListCloudWatchLogGroupsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource"`
	Region        string `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
	Pattern       string `json:"pattern,omitempty" jsonschema:"description=Optional pattern to filter log group names (prefix match)"`
	AccountId     string `json:"accountId,omitempty" jsonschema:"description=AWS account ID for cross-account monitoring."`
}

// ListCloudWatchLogGroupFieldsParams defines the parameters for listing fields in a log group
type ListCloudWatchLogGroupFieldsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the CloudWatch datasource"`
	Region        string `json:"region" jsonschema:"required,description=AWS region (e.g. us-east-1)"`
	LogGroupName  string `json:"logGroupName" jsonschema:"required,description=The log group name to discover fields for"`
	AccountId     string `json:"accountId,omitempty" jsonschema:"description=AWS account ID for cross-account monitoring."`
}

// cloudWatchLogGroupItem represents a log group returned by the log-groups resource API.
// Response format: [{"value": {"arn": "...", "name": "..."}, "accountId": "..."}]
type cloudWatchLogGroupItem struct {
	Value struct {
		ARN  string `json:"arn"`
		Name string `json:"name"`
	} `json:"value"`
	AccountId string `json:"accountId,omitempty"`
}

// cloudWatchLogGroupFieldItem represents a field returned by the log-group-fields resource API.
// Response format: [{"value": {"name": "...", "percent": 50}, "accountId": "..."}]
type cloudWatchLogGroupFieldItem struct {
	Value struct {
		Name    string `json:"name"`
		Percent int64  `json:"percent"`
	} `json:"value"`
}

// parseCloudWatchLogGroupsResponse extracts log group names from the resource API response
func parseCloudWatchLogGroupsResponse(bodyBytes []byte, bytesLimit int) ([]string, error) {
	var items []cloudWatchLogGroupItem
	if err := unmarshalJSONWithLimitMsg(bodyBytes, &items, bytesLimit); err != nil {
		return nil, err
	}

	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.Value.Name
	}
	return result, nil
}

// parseCloudWatchLogGroupFieldsResponse extracts field names from the resource API response
func parseCloudWatchLogGroupFieldsResponse(bodyBytes []byte, bytesLimit int) ([]string, error) {
	var items []cloudWatchLogGroupFieldItem
	if err := unmarshalJSONWithLimitMsg(bodyBytes, &items, bytesLimit); err != nil {
		return nil, err
	}

	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.Value.Name
	}
	return result, nil
}

// enforceCloudWatchLogsLimit ensures a limit value is within acceptable bounds
func enforceCloudWatchLogsLimit(requestedLimit int) int {
	if requestedLimit <= 0 {
		return DefaultCloudWatchLogsLimit
	}
	if requestedLimit > MaxCloudWatchLogsLimit {
		return MaxCloudWatchLogsLimit
	}
	return requestedLimit
}

// listCloudWatchLogGroups lists available CloudWatch log groups via the resource API
func listCloudWatchLogGroups(ctx context.Context, args ListCloudWatchLogGroupsParams) ([]string, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	params := url.Values{}
	if args.Region != "" {
		params.Set("region", args.Region)
	}
	if args.Pattern != "" {
		params.Set("logGroupNamePrefix", args.Pattern)
	}
	if args.AccountId != "" {
		params.Set("accountId", args.AccountId)
	}

	body, err := client.fetchCloudWatchResource(ctx, args.DatasourceUID, "log-groups", params)
	if err != nil {
		return nil, err
	}
	return parseCloudWatchLogGroupsResponse(body, 1024*1024)
}

// listCloudWatchLogGroupFields lists discovered fields for a CloudWatch log group
func listCloudWatchLogGroupFields(ctx context.Context, args ListCloudWatchLogGroupFieldsParams) ([]string, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	params := url.Values{}
	params.Set("logGroupName", args.LogGroupName)
	if args.Region != "" {
		params.Set("region", args.Region)
	}
	if args.AccountId != "" {
		params.Set("accountId", args.AccountId)
	}

	body, err := client.fetchCloudWatchResource(ctx, args.DatasourceUID, "log-group-fields", params)
	if err != nil {
		return nil, err
	}
	return parseCloudWatchLogGroupFieldsResponse(body, 1024*1024)
}

// queryCloudWatchLogs executes a CloudWatch Logs Insights query via Grafana.
// It handles the async StartQuery → poll GetQueryResults flow internally.
func queryCloudWatchLogs(ctx context.Context, args QueryCloudWatchLogsParams) (*CloudWatchLogsQueryResult, error) {
	client, err := newCloudWatchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating CloudWatch client: %w", err)
	}

	// Parse time range
	now := time.Now()
	fromTime := now.Add(-1 * time.Hour) // Default: 1 hour ago
	toTime := now                        // Default: now

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

	// Step 1: Start the query
	queryID, err := client.startLogsQuery(ctx, args, fromTime, toTime)
	if err != nil {
		return nil, fmt.Errorf("starting CloudWatch Logs query: %w", err)
	}

	// Step 2: Poll for results
	resp, err := client.pollLogsQueryResults(ctx, args.DatasourceUID, queryID, args.Region, fromTime, toTime, defaultLogsQueryTimeout)
	if err != nil {
		return nil, err
	}

	// Step 3: Parse results
	limit := enforceCloudWatchLogsLimit(args.Limit)
	result, err := parseLogsQueryResponse(resp, args.QueryString, limit)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// startLogsQuery sends the StartQuery request and extracts the queryId from the response
func (c *cloudWatchClient) startLogsQuery(ctx context.Context, args QueryCloudWatchLogsParams, from, to time.Time) (string, error) {
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

	payload := map[string]interface{}{
		"queries": []map[string]interface{}{query},
		"from":    strconv.FormatInt(from.UnixMilli(), 10),
		"to":      strconv.FormatInt(to.UnixMilli(), 10),
	}

	resp, err := c.postDsQuery(ctx, payload)
	if err != nil {
		return "", err
	}

	// Extract queryId from the response
	queryID, err := extractQueryID(resp)
	if err != nil {
		return "", fmt.Errorf("extracting queryId from StartQuery response: %w", err)
	}

	return queryID, nil
}

// pollLogsQueryResults polls GetQueryResults with exponential backoff until complete or timeout
func (c *cloudWatchClient) pollLogsQueryResults(ctx context.Context, dsUID, queryID, region string, from, to time.Time, timeout time.Duration) (*cloudWatchQueryResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	interval := initialPollInterval

	for {
		resp, err := c.getLogsQueryResults(ctx, dsUID, queryID, region, from, to)
		if err != nil {
			return nil, fmt.Errorf("polling CloudWatch Logs query results: %w", err)
		}

		status := extractQueryStatus(resp)
		switch status {
		case "Complete":
			return resp, nil
		case "Running", "Scheduled", "":
			// Continue polling
		default:
			return nil, fmt.Errorf("CloudWatch Logs query failed with status: %s", status)
		}

		// Backoff with context/timeout cancellation support
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("CloudWatch Logs query timed out after %s (last status: %s)", timeout, status)
			}
			return nil, ctx.Err()
		case <-timer.C:
		}

		interval = time.Duration(float64(interval) * pollBackoffMultiplier)
		if interval > maxPollInterval {
			interval = maxPollInterval
		}
	}
}

// getLogsQueryResults sends a GetQueryResults request for a given queryId
func (c *cloudWatchClient) getLogsQueryResults(ctx context.Context, dsUID, queryID, region string, from, to time.Time) (*cloudWatchQueryResponse, error) {
	query := map[string]interface{}{
		"datasource": map[string]string{
			"uid":  dsUID,
			"type": CloudWatchDatasourceType,
		},
		"refId":      "A",
		"type":       "logAction",
		"subtype":    "GetQueryResults",
		"queryMode":  "Logs",
		"region":     region,
		"queryId":    queryID,
		"id":         "",
		"intervalMs": 1,
	}

	payload := map[string]interface{}{
		"queries": []map[string]interface{}{query},
		"from":    strconv.FormatInt(from.UnixMilli(), 10),
		"to":      strconv.FormatInt(to.UnixMilli(), 10),
	}

	return c.postDsQuery(ctx, payload)
}

// extractQueryID extracts the queryId from a StartQuery response.
// The Grafana CloudWatch plugin returns the queryId in the first frame's data.
func extractQueryID(resp *cloudWatchQueryResponse) (string, error) {
	for _, r := range resp.Results {
		if r.Error != "" {
			return "", fmt.Errorf("query error: %s", r.Error)
		}

		for _, frame := range r.Frames {
			// Look for a field named "queryId" in the schema
			for i, field := range frame.Schema.Fields {
				if field.Name == "queryId" && i < len(frame.Data.Values) {
					if len(frame.Data.Values[i]) > 0 {
						if qid, ok := frame.Data.Values[i][0].(string); ok && qid != "" {
							return qid, nil
						}
					}
				}
			}

		}
	}

	return "", fmt.Errorf("no queryId found in StartQuery response")
}

// extractQueryStatus extracts the query status from the response frame metadata
func extractQueryStatus(resp *cloudWatchQueryResponse) string {
	for _, r := range resp.Results {
		for _, frame := range r.Frames {
			if frame.Schema.Meta != nil {
				return frame.Schema.Meta.Custom.Status
			}
		}
	}
	return ""
}

// parseLogsQueryResponse converts the raw Grafana data frame response to CloudWatchLogsQueryResult.
// The response contains columnar data: schema.fields defines column names, data.values contains parallel arrays.
func parseLogsQueryResponse(resp *cloudWatchQueryResponse, query string, limit int) (*CloudWatchLogsQueryResult, error) {
	result := &CloudWatchLogsQueryResult{
		Query: query,
		Logs:  []CloudWatchLogEntry{},
	}

	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		// Extract status from first frame's meta
		if len(r.Frames) > 0 && r.Frames[0].Schema.Meta != nil {
			result.Status = r.Frames[0].Schema.Meta.Custom.Status
		}

		for _, frame := range r.Frames {
			fieldNames := make([]string, len(frame.Schema.Fields))
			for i, f := range frame.Schema.Fields {
				fieldNames[i] = f.Name
			}

			if len(frame.Data.Values) == 0 || len(fieldNames) == 0 {
				continue
			}

			// Determine row count from first column
			rowCount := len(frame.Data.Values[0])

			for row := 0; row < rowCount; row++ {
				if len(result.Logs) >= limit {
					break
				}

				entry := CloudWatchLogEntry{
					Fields: make(map[string]string),
				}
				for col := 0; col < len(fieldNames) && col < len(frame.Data.Values); col++ {
					if row < len(frame.Data.Values[col]) {
						val := frame.Data.Values[col][row]
						// Skip Grafana-internal metadata fields
						if fieldNames[col] == "@ptr" || strings.HasSuffix(fieldNames[col], "__grafana_internal__") {
							continue
						}
						entry.Fields[fieldNames[col]] = formatLogValue(val, frame.Schema.Fields[col].Type)
					}
				}
				result.Logs = append(result.Logs, entry)
			}
		}
	}

	result.TotalFound = len(result.Logs)

	if len(result.Logs) == 0 {
		result.Hints = generateCloudWatchLogsEmptyResultHints()
	}

	return result, nil
}

// formatLogValue converts a raw interface{} value to a string for display.
// It handles timestamps (float64 ms → RFC3339), strings, and nil values.
func formatLogValue(v interface{}, fieldType string) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if fieldType == "time" {
			return time.UnixMilli(int64(val)).UTC().Format(time.RFC3339Nano)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int64:
		if fieldType == "time" {
			return time.UnixMilli(val).UTC().Format(time.RFC3339Nano)
		}
		return strconv.FormatInt(val, 10)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

// generateCloudWatchLogsEmptyResultHints generates helpful hints when a Logs Insights query returns no data
func generateCloudWatchLogsEmptyResultHints() []string {
	return []string{
		"No log data found. Possible reasons:",
		"- Log group name may be incorrect - use list_cloudwatch_log_groups to discover available groups",
		"- Query syntax may be invalid - check CloudWatch Logs Insights query syntax",
		"- Filter may be too restrictive - try a broader filter or remove it",
		"- Time range may have no log events - try extending with start=\"now-6h\"",
		"- Region may be incorrect - verify the log group exists in the specified region",
		"- Use list_cloudwatch_log_group_fields to discover available fields for the log group",
	}
}

// Tool definitions

// QueryCloudWatchLogs is a tool for executing CloudWatch Logs Insights queries via Grafana
var QueryCloudWatchLogs = mcpgrafana.MustTool(
	"query_cloudwatch_logs",
	`Execute a CloudWatch Logs Insights query via Grafana. Requires region and at least one log group.

REQUIRED FIRST: Use list_cloudwatch_log_groups -> list_cloudwatch_log_group_fields -> then query.

The query uses CloudWatch Logs Insights syntax:
- fields @timestamp, @message | sort @timestamp desc | limit 20
- filter @message like /error/i | stats count() by bin(5m)
- fields @timestamp, @message, @logStream | filter @message like /exception/

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)

Cross-account monitoring: Use the accountId parameter in list tools to discover log groups from linked accounts.`,
	queryCloudWatchLogs,
	mcp.WithTitleAnnotation("Query CloudWatch Logs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListCloudWatchLogGroups is a tool for listing CloudWatch log groups
var ListCloudWatchLogGroups = mcpgrafana.MustTool(
	"list_cloudwatch_log_groups",
	"START HERE for CloudWatch Logs: List available log groups. Requires region. Supports filtering by prefix pattern and cross-account monitoring via optional accountId. NEXT: Use list_cloudwatch_log_group_fields, then query_cloudwatch_logs.",
	listCloudWatchLogGroups,
	mcp.WithTitleAnnotation("List CloudWatch log groups"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListCloudWatchLogGroupFields is a tool for listing fields in a CloudWatch log group
var ListCloudWatchLogGroupFields = mcpgrafana.MustTool(
	"list_cloudwatch_log_group_fields",
	"List discovered fields for a CloudWatch log group. Use after list_cloudwatch_log_groups to find available fields for querying. Requires region. NEXT: Use query_cloudwatch_logs with the discovered fields.",
	listCloudWatchLogGroupFields,
	mcp.WithTitleAnnotation("List CloudWatch log group fields"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
