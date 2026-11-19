package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/internal/datasource"
)

const (
	XRayDatasourceType = "grafana-x-ray-datasource"

	DefaultXRayTraceSummariesLimit = 20

	MaxXRayTraceSummariesLimit = 100
)

// newXRayClient validates the datasource type and returns an /api/ds/query client.
func newXRayClient(ctx context.Context, uid string) (*http.Client, string, error) {
	ds, err := datasource.ByUID(ctx, uid)
	if err != nil {
		return nil, "", err
	}
	if ds.Type != XRayDatasourceType {
		return nil, "", fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, XRayDatasourceType)
	}
	return newDSQueryHTTPClient(ctx)
}

// buildXRayQuery builds an /api/ds/query query for one of the plugin's X-Ray
// query types. query is the filter expression or the trace ID.
func buildXRayQuery(datasourceUID, queryType, query, region string) map[string]interface{} {
	q := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"uid":  datasourceUID,
			"type": XRayDatasourceType,
		},
		"queryMode": "X-Ray",
		"queryType": queryType,
		"query":     query,
	}
	if region != "" {
		q["region"] = region
	}
	return q
}

func runXRayQuery(ctx context.Context, datasourceUID string, query map[string]interface{}, from, to time.Time) (data.Frames, error) {
	client, baseURL, err := newXRayClient(ctx, datasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating X-Ray client: %w", err)
	}
	resp, err := doDSQuery(ctx, client, baseURL, dsQueryPayload(from, to, query))
	if err != nil {
		return nil, err
	}
	return xrayQueryFrames(resp)
}

// xrayQueryFrames returns the frames for refId A, or that query's error.
func xrayQueryFrames(resp *backend.QueryDataResponse) (data.Frames, error) {
	r, ok := resp.Responses["A"]
	if !ok {
		return nil, errors.New("no result for query refId A")
	}
	if r.Error != nil {
		return nil, fmt.Errorf("query error: %w", r.Error)
	}
	return r.Frames, nil
}

// SearchXRayTracesParams defines the parameters for search_xray_traces.
type SearchXRayTracesParams struct {
	DatasourceUID    string `json:"datasourceUid" jsonschema:"required,description=The UID of the X-Ray datasource (type grafana-x-ray-datasource)."`
	FilterExpression string `json:"filterExpression,omitempty" jsonschema:"description=X-Ray filter expression\\, e.g. service(\"checkout\") AND fault = true. Omit to match every trace."`
	Start            string `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End              string `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	Region           string `json:"region,omitempty" jsonschema:"description=AWS region (e.g. us-east-1). Default: the datasource's default region."`
	Limit            int    `json:"limit,omitempty" jsonschema:"description=Maximum number of traces to return. Default: 20\\, max: 100."`
}

// XRayTraceSummary is one trace returned by search_xray_traces.
type XRayTraceSummary struct {
	ID         string  `json:"id"`
	StartTime  string  `json:"startTime,omitempty"`
	DurationMs float64 `json:"durationMs"`
	Method     string  `json:"method,omitempty"`
	URL        string  `json:"url,omitempty"`
	Status     int     `json:"status,omitempty"`
}

// SearchXRayTracesResult holds the newest matching traces.
type SearchXRayTracesResult struct {
	Traces    []XRayTraceSummary `json:"traces"`
	Truncated bool               `json:"truncated"`
}

// parseXRayTraceSummaries reads the plugin's TraceSummaries frame, newest first.
func parseXRayTraceSummaries(frames data.Frames, limit int) *SearchXRayTracesResult {
	traces := []XRayTraceSummary{}
	for _, frame := range frames {
		for row := 0; row < frame.Rows(); row++ {
			s := XRayTraceSummary{}
			for _, field := range frame.Fields {
				v, ok := field.ConcreteAt(row)
				if !ok {
					continue
				}
				switch field.Name {
				case "Id":
					s.ID, _ = v.(string)
				case "Start Time":
					if t, ok := v.(time.Time); ok {
						s.StartTime = t.UTC().Format(time.RFC3339Nano)
					}
				case "Method":
					s.Method, _ = v.(string)
				case "URL":
					s.URL, _ = v.(string)
				case "Response":
					if status, ok := v.(int32); ok {
						s.Status = int(status)
					}
				case "Response Time":
					if seconds, ok := v.(float64); ok {
						s.DurationMs = seconds * 1000
					}
				}
			}
			if s.ID != "" {
				traces = append(traces, s)
			}
		}
	}

	// RFC3339 in UTC sorts lexically.
	slices.SortStableFunc(traces, func(a, b XRayTraceSummary) int { return strings.Compare(b.StartTime, a.StartTime) })

	result := &SearchXRayTracesResult{Traces: traces}
	if len(traces) > limit {
		result.Traces = traces[:limit]
		result.Truncated = true
	}
	return result
}

func searchXRayTraces(ctx context.Context, args SearchXRayTracesParams) (*SearchXRayTracesResult, error) {
	now := time.Now()
	from, to := now.Add(-time.Hour), now
	if args.Start != "" {
		parsed, err := parseStartTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			from = parsed
		}
	}
	if args.End != "" {
		parsed, err := parseEndTime(args.End)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			to = parsed
		}
	}
	if !to.After(from) {
		return nil, fmt.Errorf("end time must be after start time")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = DefaultXRayTraceSummariesLimit
	}
	limit = min(limit, MaxXRayTraceSummariesLimit)

	query := buildXRayQuery(args.DatasourceUID, "getTraceSummaries", args.FilterExpression, args.Region)
	frames, err := runXRayQuery(ctx, args.DatasourceUID, query, from, to)
	if err != nil {
		return nil, err
	}
	return parseXRayTraceSummaries(frames, limit), nil
}

var SearchXRayTraces = mcpgrafana.MustTool(
	"search_xray_traces",
	`Search AWS X-Ray traces through an X-Ray datasource with an X-Ray filter expression. Returns the newest matching traces with ID, start time, duration, and HTTP method, URL and status.

The datasource reads at most 1000 summaries per search; 'truncated' is true when more traces matched than 'limit'. Narrow the time range or the filter to find older ones.

Filter examples:
  service("checkout") AND fault = true
  http.status = 502
  responsetime > 3
  annotation.customer_id = "123"`,
	searchXRayTraces,
	mcpgrafana.WithTitleAnnotation("Search X-Ray traces"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// GetXRayTraceParams defines the parameters for get_xray_trace.
type GetXRayTraceParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the X-Ray datasource (type grafana-x-ray-datasource)."`
	TraceID       string `json:"traceId" jsonschema:"required,description=Trace ID in X-Ray (1-5759e988-bd862e3fe1be46a994272793) or W3C (5759e988bd862e3fe1be46a994272793) format."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region (e.g. us-east-1). Default: the datasource's default region."`
}

// XRayTraceNode is a segment or subsegment of a trace.
type XRayTraceNode struct {
	Name          string           `json:"name"`
	Origin        string           `json:"origin,omitempty"`
	StartOffsetMs float64          `json:"startOffsetMs"`
	DurationMs    float64          `json:"durationMs"`
	HTTPStatus    int              `json:"httpStatus,omitempty"`
	Error         bool             `json:"error,omitempty"`
	Fault         bool             `json:"fault,omitempty"`
	Throttle      bool             `json:"throttle,omitempty"`
	Exception     string           `json:"exception,omitempty"`
	Children      []*XRayTraceNode `json:"children,omitempty"`
}

// XRayTraceResult is a trace as a tree of segments, with offsets relative to its start.
type XRayTraceResult struct {
	TraceID    string           `json:"traceId"`
	StartTime  string           `json:"startTime,omitempty"`
	DurationMs float64          `json:"durationMs"`
	Segments   []*XRayTraceNode `json:"segments"`
}

// xrayTrace is the JSON the plugin returns in the Traces frame: an
// aws-sdk-go-v2 types.Trace, which has no JSON tags.
type xrayTrace struct {
	ID       string  `json:"Id"`
	Duration float64 `json:"Duration"`
	Segments []struct {
		Document string `json:"Document"`
	} `json:"Segments"`
}

// xraySegmentDocument holds the fields of an X-Ray segment document the tool reads.
type xraySegmentDocument struct {
	ID        string  `json:"id"`
	ParentID  string  `json:"parent_id"`
	Name      string  `json:"name"`
	Origin    string  `json:"origin"`
	Inferred  bool    `json:"inferred"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	HTTP      struct {
		Response struct {
			Status int `json:"status"`
		} `json:"response"`
	} `json:"http"`
	Error       bool                  `json:"error"`
	Fault       bool                  `json:"fault"`
	Throttle    bool                  `json:"throttle"`
	Cause       json.RawMessage       `json:"cause"`
	Subsegments []xraySegmentDocument `json:"subsegments"`
}

// xrayExceptionMessage returns the first exception of a cause object. A cause
// can also be the ID of an exception recorded elsewhere, which yields "".
func xrayExceptionMessage(cause json.RawMessage) string {
	var c struct {
		Exceptions []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"exceptions"`
	}
	if json.Unmarshal(cause, &c) != nil {
		return ""
	}
	for _, e := range c.Exceptions {
		switch {
		case e.Type != "" && e.Message != "":
			return e.Type + ": " + e.Message
		case e.Message != "":
			return e.Message
		case e.Type != "":
			return e.Type
		}
	}
	return ""
}

// xrayMs converts seconds to milliseconds, rounded to the microsecond
// precision of X-Ray timestamps.
func xrayMs(seconds float64) float64 {
	return math.Round(seconds*1e6) / 1e3
}

func xrayTraceNode(doc xraySegmentDocument, traceStart float64, byID map[string]*XRayTraceNode) *XRayTraceNode {
	n := &XRayTraceNode{
		Name:          doc.Name,
		Origin:        doc.Origin,
		StartOffsetMs: xrayMs(doc.StartTime - traceStart),
		DurationMs:    xrayMs(max(doc.EndTime-doc.StartTime, 0)),
		HTTPStatus:    doc.HTTP.Response.Status,
		Error:         doc.Error,
		Fault:         doc.Fault,
		Throttle:      doc.Throttle,
		Exception:     xrayExceptionMessage(doc.Cause),
	}
	if doc.ID != "" {
		byID[doc.ID] = n
	}
	for _, sub := range doc.Subsegments {
		n.Children = append(n.Children, xrayTraceNode(sub, traceStart, byID))
	}
	return n
}

// parseXRayTrace builds the segment tree from the plugin's Traces frame. A
// segment called by another service hangs under the subsegment that called it.
// Inferred segments, which X-Ray derives from a subsegment's downstream call
// for the service map, repeat that subsegment and are left out.
func parseXRayTrace(frames data.Frames) (*XRayTraceResult, error) {
	var raw string
	for _, frame := range frames {
		if f, _ := frame.FieldByName("Trace"); f != nil && f.Len() > 0 {
			if v, ok := f.ConcreteAt(0); ok {
				raw, _ = v.(string)
			}
		}
	}
	if raw == "" {
		return nil, errors.New("no trace found in the X-Ray response")
	}

	var trace xrayTrace
	if err := json.Unmarshal([]byte(raw), &trace); err != nil {
		return nil, fmt.Errorf("parsing trace: %w", err)
	}

	docs := make([]xraySegmentDocument, 0, len(trace.Segments))
	for _, s := range trace.Segments {
		var doc xraySegmentDocument
		if err := json.Unmarshal([]byte(s.Document), &doc); err != nil {
			return nil, fmt.Errorf("parsing segment document: %w", err)
		}
		if !doc.Inferred {
			docs = append(docs, doc)
		}
	}

	result := &XRayTraceResult{TraceID: trace.ID, DurationMs: xrayMs(trace.Duration), Segments: []*XRayTraceNode{}}
	if len(docs) == 0 {
		return result, nil
	}

	traceStart := slices.MinFunc(docs, func(a, b xraySegmentDocument) int { return cmp.Compare(a.StartTime, b.StartTime) }).StartTime
	result.StartTime = time.UnixMicro(int64(traceStart * 1e6)).UTC().Format(time.RFC3339Nano)

	byID := map[string]*XRayTraceNode{}
	nodes := make([]*XRayTraceNode, len(docs))
	for i, doc := range docs {
		nodes[i] = xrayTraceNode(doc, traceStart, byID)
	}
	for i, doc := range docs {
		if parent, ok := byID[doc.ParentID]; ok && parent != nodes[i] {
			parent.Children = append(parent.Children, nodes[i])
		} else {
			result.Segments = append(result.Segments, nodes[i])
		}
	}

	byStart := func(a, b *XRayTraceNode) int { return cmp.Compare(a.StartOffsetMs, b.StartOffsetMs) }
	slices.SortStableFunc(result.Segments, byStart)
	for _, n := range byID {
		slices.SortStableFunc(n.Children, byStart)
	}
	return result, nil
}

func getXRayTrace(ctx context.Context, args GetXRayTraceParams) (*XRayTraceResult, error) {
	if strings.TrimSpace(args.TraceID) == "" {
		return nil, errors.New("traceId is required")
	}
	// BatchGetTraces has no time range; /api/ds/query requires one.
	now := time.Now()
	query := buildXRayQuery(args.DatasourceUID, "getTrace", strings.TrimSpace(args.TraceID), args.Region)
	frames, err := runXRayQuery(ctx, args.DatasourceUID, query, now.Add(-time.Hour), now)
	if err != nil {
		return nil, err
	}
	return parseXRayTrace(frames)
}

var GetXRayTrace = mcpgrafana.MustTool(
	"get_xray_trace",
	`Get one AWS X-Ray trace through an X-Ray datasource as a tree of segments and subsegments. Each node has its name, origin (e.g. AWS::ECS::Container, AWS::Lambda::Function), start offset and duration in milliseconds, HTTP status, error/fault/throttle flags, and the exception message when one was recorded. A downstream service's segment appears under the subsegment that called it.

Accepts X-Ray (1-5759e988-bd862e3fe1be46a994272793) or W3C (5759e988bd862e3fe1be46a994272793) trace IDs, such as the @xrayTraceId field of CloudWatch logs.`,
	getXRayTrace,
	mcpgrafana.WithTitleAnnotation("Get X-Ray trace"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)
