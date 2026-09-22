package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	CloudLoggingDatasourceType = "googlecloud-logging-datasource"

	DefaultCloudLoggingLimit = 100

	MaxCloudLoggingLimit = 1000
)

// cloudLoggingClient queries a Google Cloud Logging datasource through Grafana.
type cloudLoggingClient struct {
	httpClient *http.Client
	baseURL    string
	uid        string
}

// newCloudLoggingClient validates the datasource type and returns a client for it.
func newCloudLoggingClient(ctx context.Context, uid string) (*cloudLoggingClient, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	if ds.Type != CloudLoggingDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, CloudLoggingDatasourceType)
	}

	httpClient, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}

	return &cloudLoggingClient{httpClient: httpClient, baseURL: baseURL, uid: uid}, nil
}

// resource calls one of the plugin's resource routes and returns the body.
func (c *cloudLoggingClient) resource(ctx context.Context, path string, params url.Values) ([]byte, error) {
	resourceURL := c.baseURL + "/api/datasources/uid/" + url.PathEscape(c.uid) + "/resources" + path
	if len(params) > 0 {
		resourceURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("cloud logging resource %s returned status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	bodyBytes, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	return bodyBytes, nil
}

// resourceStrings calls resource and decodes a JSON string array.
func (c *cloudLoggingClient) resourceStrings(ctx context.Context, path string, params url.Values) ([]string, error) {
	body, err := c.resource(ctx, path, params)
	if err != nil {
		return nil, err
	}
	return parseCloudLoggingStringList(body)
}

// parseCloudLoggingStringList decodes a JSON string array, mapping null to an empty slice.
func parseCloudLoggingStringList(body []byte) ([]string, error) {
	var items []string
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}
	if items == nil {
		items = []string{}
	}
	return items, nil
}

// CloudLoggingQueryParams defines the parameters for query_cloud_logging.
type CloudLoggingQueryParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Google Cloud Logging datasource. Use list_datasources (type googlecloud-logging-datasource) to find it."`
	ProjectID     string `json:"projectId" jsonschema:"required,description=GCP project ID to read logs from (e.g. my-prod-project). Use list_cloud_logging_projects if unknown."`
	Filter        string `json:"filter,omitempty" jsonschema:"description=Cloud Logging query language filter\\, e.g. resource.type=\"k8s_container\" AND severity>=ERROR. Leave empty to return all logs in the time range. Do NOT add timestamp clauses: the time range is applied automatically from start/end."`
	BucketID      string `json:"bucketId,omitempty" jsonschema:"description=Optional log bucket to scope the query\\, exactly as returned by list_cloud_logging_buckets (e.g. global/buckets/_Default). Omit to search the project's default scope."`
	ViewID        string `json:"viewId,omitempty" jsonschema:"description=Optional log view within bucketId\\, as returned by list_cloud_logging_views. Only meaningful when bucketId is set; defaults to _AllLogs."`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End           string `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	Limit         int    `json:"limit,omitempty" jsonschema:"description=Maximum number of log entries to return\\, newest first. Default: 100\\, max: 1000."`
}

// CloudLoggingEntry is one log entry returned by query_cloud_logging.
type CloudLoggingEntry struct {
	Timestamp time.Time       `json:"timestamp"`
	Severity  string          `json:"severity,omitempty"`
	Body      string          `json:"body"`
	ID        string          `json:"id,omitempty"`
	TraceID   string          `json:"traceId,omitempty"`
	Labels    json.RawMessage `json:"labels,omitempty"`
}

// CloudLoggingQueryResult is the normalized result returned to the MCP client.
type CloudLoggingQueryResult struct {
	Entries    []CloudLoggingEntry `json:"entries"`
	EntryCount int                 `json:"entryCount"`
	Limit      int                 `json:"limit"`
	Truncated  bool                `json:"truncated"`
	Hints      *EmptyResultHints   `json:"hints,omitempty"`
}

// normalizeCloudLoggingLimit applies the default limit and the maximum.
func normalizeCloudLoggingLimit(limit int) int {
	if limit <= 0 {
		return DefaultCloudLoggingLimit
	}
	if limit > MaxCloudLoggingLimit {
		return MaxCloudLoggingLimit
	}
	return limit
}

// buildCloudLoggingPayload constructs the /api/ds/query request body.
func buildCloudLoggingPayload(datasourceUID, projectID, filter, bucketID, viewID string, from, to time.Time, limit int) map[string]interface{} {
	q := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"uid":  datasourceUID,
			"type": CloudLoggingDatasourceType,
		},
		"queryText":     filter,
		"projectId":     projectID,
		"bucketId":      bucketID,
		"viewId":        viewID,
		"maxDataPoints": normalizeCloudLoggingLimit(limit),
	}
	return dsQueryPayload(from, to, q)
}

// cloudLoggingEntriesFromResponse converts the response frames into entries.
func cloudLoggingEntriesFromResponse(resp *backend.QueryDataResponse) ([]CloudLoggingEntry, error) {
	entries := []CloudLoggingEntry{}

	refIDs := make([]string, 0, len(resp.Responses))
	for refID := range resp.Responses {
		refIDs = append(refIDs, refID)
	}
	sort.Strings(refIDs)

	for _, refID := range refIDs {
		r := resp.Responses[refID]
		if r.Error != nil {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}
		for _, frame := range r.Frames {
			if frame == nil {
				continue
			}
			entries = append(entries, cloudLoggingEntriesFromFrame(frame)...)
		}
	}
	return entries, nil
}

func cloudLoggingEntriesFromFrame(frame *data.Frame) []CloudLoggingEntry {
	fieldByName := make(map[string]*data.Field, len(frame.Fields))
	for _, f := range frame.Fields {
		if f != nil {
			fieldByName[strings.ToLower(f.Name)] = f
		}
	}

	n := frame.Rows()
	entries := make([]CloudLoggingEntry, 0, n)
	for i := 0; i < n; i++ {
		e := CloudLoggingEntry{}
		if f := fieldByName["timestamp"]; f != nil {
			if ts, ok := f.ConcreteAt(i); ok {
				if t, ok := ts.(time.Time); ok {
					e.Timestamp = t
				}
			}
		}
		e.Body = fieldString(fieldByName["body"], i)
		e.Severity = fieldString(fieldByName["severity"], i)
		e.ID = fieldString(fieldByName["id"], i)
		e.TraceID = fieldString(fieldByName["traceid"], i)
		if f := fieldByName["labels"]; f != nil {
			if v, ok := f.ConcreteAt(i); ok {
				switch lv := v.(type) {
				case json.RawMessage:
					if len(lv) > 0 && string(lv) != "null" {
						e.Labels = lv
					}
				case string:
					if lv != "" && lv != "null" && json.Valid([]byte(lv)) {
						e.Labels = json.RawMessage(lv)
					}
				}
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// fieldString returns the string value of field f at row i, or "" if absent.
func fieldString(f *data.Field, i int) string {
	if f == nil {
		return ""
	}
	v, ok := f.ConcreteAt(i)
	if !ok {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case json.RawMessage:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}

func queryCloudLogging(ctx context.Context, args CloudLoggingQueryParams) (*CloudLoggingQueryResult, error) {
	if strings.TrimSpace(args.ProjectID) == "" {
		return nil, fmt.Errorf("projectId is required")
	}
	if args.ViewID != "" && args.BucketID == "" {
		return nil, fmt.Errorf("viewId requires bucketId")
	}

	client, err := newCloudLoggingClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Cloud Logging client: %w", err)
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
	if !toTime.After(fromTime) {
		return nil, fmt.Errorf("end time must be after start time")
	}

	limit := normalizeCloudLoggingLimit(args.Limit)
	payload := buildCloudLoggingPayload(args.DatasourceUID, args.ProjectID, args.Filter, args.BucketID, args.ViewID, fromTime, toTime, limit)

	resp, err := doDSQuery(ctx, client.httpClient, client.baseURL, payload)
	if err != nil {
		return nil, err
	}

	entries, err := cloudLoggingEntriesFromResponse(resp)
	if err != nil {
		return nil, err
	}

	result := &CloudLoggingQueryResult{
		Entries:    entries,
		EntryCount: len(entries),
		Limit:      limit,
		Truncated:  len(entries) >= limit,
	}

	if result.EntryCount == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "cloudlogging",
			Query:          args.Filter,
			StartTime:      fromTime,
			EndTime:        toTime,
		})
	}
	return result, nil
}

var QueryCloudLogging = mcpgrafana.MustTool(
	"query_cloud_logging",
	`Query logs from a Google Cloud Logging datasource using the Cloud Logging query language. Returns entries newest-first with timestamp, severity, body, log entry id, trace id, and labels (resource/log labels as JSON).

The time range (start/end, default last hour) is applied automatically: do NOT put timestamp clauses in the filter. Results are capped at 'limit' (default 100, max 1000); 'truncated' is true when the cap was hit, so narrow the filter or time range to see more.

Filter examples:
  resource.type="k8s_container" AND resource.labels.namespace_name="payments" AND severity>=ERROR
  logName:"stderr" AND jsonPayload.message:"timeout"
  resource.type="cloud_run_revision" AND httpRequest.status>=500
  labels."k8s-pod/app"="api" AND NOT textPayload:"healthz"

If the project is unknown, call list_cloud_logging_projects first. Pass bucketId (from list_cloud_logging_buckets) to read a specific log bucket, optionally with viewId.`,
	queryCloudLogging,
	mcp.WithTitleAnnotation("Query Google Cloud Logging"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// ListCloudLoggingProjectsParams defines the parameters for listing GCP projects.
type ListCloudLoggingProjectsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Google Cloud Logging datasource"`
	Query         string `json:"query,omitempty" jsonschema:"description=Optional substring to filter project IDs (e.g. prod)."`
}

func listCloudLoggingProjects(ctx context.Context, args ListCloudLoggingProjectsParams) ([]string, error) {
	client, err := newCloudLoggingClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Cloud Logging client: %w", err)
	}

	params := url.Values{}
	if args.Query != "" {
		params.Set("query", args.Query)
	}
	return client.resourceStrings(ctx, "/projects", params)
}

var ListCloudLoggingProjects = mcpgrafana.MustTool(
	"list_cloud_logging_projects",
	"START HERE for Google Cloud Logging: List the GCP project IDs the datasource's credentials can read logs from (at most 100; use 'query' to narrow). NEXT: pass a projectId to query_cloud_logging, or to list_cloud_logging_buckets to scope by log bucket.",
	listCloudLoggingProjects,
	mcp.WithTitleAnnotation("List Cloud Logging projects"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// ListCloudLoggingBucketsParams defines the parameters for listing log buckets.
type ListCloudLoggingBucketsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Google Cloud Logging datasource"`
	ProjectID     string `json:"projectId" jsonschema:"required,description=GCP project ID whose log buckets to list"`
}

func listCloudLoggingBuckets(ctx context.Context, args ListCloudLoggingBucketsParams) ([]string, error) {
	if strings.TrimSpace(args.ProjectID) == "" {
		return nil, fmt.Errorf("projectId is required")
	}
	client, err := newCloudLoggingClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Cloud Logging client: %w", err)
	}

	params := url.Values{}
	params.Set("ProjectId", args.ProjectID)
	return client.resourceStrings(ctx, "/logbuckets", params)
}

var ListCloudLoggingBuckets = mcpgrafana.MustTool(
	"list_cloud_logging_buckets",
	"List the log buckets in a GCP project, as '<location>/buckets/<name>' (e.g. global/buckets/_Default). Pass a value verbatim as bucketId to query_cloud_logging or list_cloud_logging_views.",
	listCloudLoggingBuckets,
	mcp.WithTitleAnnotation("List Cloud Logging buckets"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// ListCloudLoggingViewsParams defines the parameters for listing log views.
type ListCloudLoggingViewsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Google Cloud Logging datasource"`
	ProjectID     string `json:"projectId" jsonschema:"required,description=GCP project ID that owns the bucket"`
	BucketID      string `json:"bucketId" jsonschema:"required,description=Log bucket exactly as returned by list_cloud_logging_buckets (e.g. global/buckets/_Default)"`
}

func listCloudLoggingViews(ctx context.Context, args ListCloudLoggingViewsParams) ([]string, error) {
	if strings.TrimSpace(args.ProjectID) == "" {
		return nil, fmt.Errorf("projectId is required")
	}
	if strings.TrimSpace(args.BucketID) == "" {
		return nil, fmt.Errorf("bucketId is required")
	}
	client, err := newCloudLoggingClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Cloud Logging client: %w", err)
	}

	params := url.Values{}
	params.Set("ProjectId", args.ProjectID)
	params.Set("BucketId", args.BucketID)
	return client.resourceStrings(ctx, "/logviews", params)
}

var ListCloudLoggingViews = mcpgrafana.MustTool(
	"list_cloud_logging_views",
	"List the log views in a log bucket (e.g. _AllLogs, _Default). Pass one as viewId, together with the same bucketId, to query_cloud_logging.",
	listCloudLoggingViews,
	mcp.WithTitleAnnotation("List Cloud Logging views"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddCloudLoggingTools registers the Google Cloud Logging tools with the MCP server.
func AddCloudLoggingTools(mcp *server.MCPServer, enableQueryTools bool) {
	if enableQueryTools {
		QueryCloudLogging.Register(mcp)
	}
	ListCloudLoggingProjects.Register(mcp)
	ListCloudLoggingBuckets.Register(mcp)
	ListCloudLoggingViews.Register(mcp)
}
