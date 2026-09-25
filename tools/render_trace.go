package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxTraceIDLength      = 32
	maxDatasourceUIDSize  = 256
	maxTraceSpans         = 100_000
	maxTraceResponseBytes = 64 << 20
	maxTraceResultBytes   = 32 << 20
)

// RenderTraceParams identifies the Tempo trace to fetch and the span the app
// should select initially. The trace remains complete when focus_span_id is set.
type RenderTraceParams struct {
	TraceID       string  `json:"trace_id" jsonschema:"required,description=Tempo trace ID as 16 or 32 hexadecimal characters."`
	DatasourceUID string  `json:"datasource_uid" jsonschema:"required,description=UID of the Tempo datasource that contains the trace."`
	FocusSpanID   *string `json:"focus_span_id,omitempty" jsonschema:"description=Optional 16-character hexadecimal span ID to select initially. The full trace is still returned."`
}

// RenderTraceResult is the structured content consumed by the trace MCP App.
type RenderTraceResult struct {
	TraceID       string      `json:"traceId"`
	DatasourceUID string      `json:"datasourceUid"`
	FocusSpanID   *string     `json:"focusSpanId,omitempty"`
	GrafanaURL    string      `json:"grafanaUrl"`
	Spans         []TraceSpan `json:"spans"`
}

// TraceSpan is a display-oriented, JSON-safe representation of an OTLP span.
type TraceSpan struct {
	ID          string         `json:"id"`
	ParentID    *string        `json:"parentId,omitempty"`
	Name        string         `json:"name"`
	ServiceName string         `json:"serviceName"`
	StartTimeMS float64        `json:"startTimeMs"`
	DurationMS  float64        `json:"durationMs"`
	Status      string         `json:"status"`
	Attributes  map[string]any `json:"attributes"`
	Events      []TraceEvent   `json:"events"`
}

// TraceEvent preserves every event attribute, including exception type,
// message, and stack trace fields.
type TraceEvent struct {
	Name       string         `json:"name"`
	TimeMS     float64        `json:"timeMs"`
	Attributes map[string]any `json:"attributes"`
}

// RenderTraceTool loads a complete Tempo trace for the interactive trace viewer.
var RenderTraceTool = mcpgrafana.MustTool(
	"render_trace",
	"Fetch a Tempo trace by ID and display it as an interactive span waterfall. Use focus_span_id to open a specific span while preserving the complete trace.",
	renderTrace,
	mcp.WithTitleAnnotation("Display trace"),
	mcp.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithUIResource(mcpgrafana.TraceViewerResourceURI),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddTraceAppTools registers the trace viewer tool when datasource queries are enabled.
func AddTraceAppTools(s *server.MCPServer, enableQueryTools bool) {
	if enableQueryTools {
		RenderTraceTool.Register(s)
	}
}

func renderTrace(ctx context.Context, args RenderTraceParams) (*mcp.CallToolResult, error) {
	if err := validateRenderTraceParams(args); err != nil {
		return nil, err
	}
	args.TraceID = strings.ToLower(args.TraceID)
	if args.FocusSpanID != nil {
		focusSpanID := strings.ToLower(*args.FocusSpanID)
		args.FocusSpanID = &focusSpanID
	}

	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	if strings.TrimSpace(config.URL) == "" {
		return nil, fmt.Errorf("grafana URL is not available in the authenticated request context")
	}
	if _, err := tempoProxyURL(config.URL, args.DatasourceUID); err != nil {
		return nil, err
	}
	backend, err := tempoBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	trace, err := fetchTrace(ctx, backend, args.TraceID)
	if err != nil {
		return nil, fmt.Errorf("fetch trace %q from Tempo datasource %q: %w", args.TraceID, args.DatasourceUID, err)
	}

	spans, err := normalizeTrace(trace)
	if err != nil {
		return nil, err
	}
	grafanaURL := ""
	if deeplinkResolvesInRenderOrg(ctx, config.OrgID) {
		publicURL, err := grafanaBaseURLFromContext(ctx)
		if err != nil {
			return nil, err
		}
		grafanaURL, err = traceExploreURL(publicURL, mcpgrafana.GrafanaVersion(ctx), args)
		if err != nil {
			return nil, err
		}
	}
	result := RenderTraceResult{
		TraceID:       args.TraceID,
		DatasourceUID: args.DatasourceUID,
		FocusSpanID:   args.FocusSpanID,
		GrafanaURL:    grafanaURL,
		Spans:         spans,
	}
	if err := validateTraceResultSize(result); err != nil {
		return nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(fmt.Sprintf(
			"Loaded trace %s from datasource %s with %d spans.",
			args.TraceID,
			args.DatasourceUID,
			len(spans),
		))},
		StructuredContent: result,
	}, nil
}

func validateRenderTraceParams(args RenderTraceParams) error {
	if !isHexID(args.TraceID, 16, maxTraceIDLength) {
		return fmt.Errorf("trace_id must be 16 or 32 hexadecimal characters")
	}
	if err := validateDatasourceUID(args.DatasourceUID); err != nil {
		return err
	}
	if args.FocusSpanID != nil && !isHexID(*args.FocusSpanID, 16, 16) {
		return fmt.Errorf("focus_span_id must be 16 hexadecimal characters")
	}
	return nil
}

func isHexID(value string, lengths ...int) bool {
	validLength := false
	for _, length := range lengths {
		if len(value) == length {
			validLength = true
			break
		}
	}
	if !validLength {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func validateDatasourceUID(uid string) error {
	if uid == "" {
		return fmt.Errorf("datasource_uid is required")
	}
	if len(uid) > maxDatasourceUIDSize {
		return fmt.Errorf("datasource_uid must be at most %d bytes", maxDatasourceUIDSize)
	}
	if uid == "." || uid == ".." || strings.ContainsAny(uid, "/\\?#") {
		return fmt.Errorf("datasource_uid contains invalid path characters")
	}
	for _, r := range uid {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("datasource_uid must not contain whitespace or control characters")
		}
	}
	return nil
}

func tempoProxyURL(grafanaURL, datasourceUID string) (string, error) {
	u, err := url.Parse(grafanaURL)
	if err != nil {
		return "", fmt.Errorf("parse Grafana URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("grafana URL must be an absolute HTTP or HTTPS URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("grafana URL must use HTTP or HTTPS")
	}
	u.Path = path.Join(u.Path, "api", "datasources", "proxy", "uid", datasourceUID)
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func fetchTrace(ctx context.Context, backend *tempoBackend, traceID string) (*tracepb.TracesData, error) {
	u, err := url.Parse(backend.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Tempo proxy URL: %w", err)
	}
	u.Path = path.Join(u.Path, "api", "v2", "traces", traceID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Tempo request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := backend.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute Tempo request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("trace not found")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("tempo request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return decodeTraceResponse(resp.Body, maxTraceResponseBytes)
}

func decodeTraceResponse(reader io.Reader, maxBytes int64) (*tracepb.TracesData, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Tempo response: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("tempo response exceeds the supported limit of %d bytes; no span or exception data was truncated", maxBytes)
	}

	var envelope struct {
		Trace json.RawMessage `json:"trace"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode Tempo response: %w", err)
	}
	if len(envelope.Trace) == 0 || string(envelope.Trace) == "null" {
		return nil, fmt.Errorf("tempo response did not contain a trace")
	}
	traceData := &tracepb.TracesData{}
	if err := protojson.Unmarshal(envelope.Trace, traceData); err != nil {
		return nil, fmt.Errorf("decode Tempo OTLP trace: %w", err)
	}
	return traceData, nil
}

func normalizeTrace(trace *tracepb.TracesData) ([]TraceSpan, error) {
	if trace == nil {
		return nil, fmt.Errorf("tempo returned an empty trace")
	}

	spanCount := 0
	for _, resourceSpans := range trace.ResourceSpans {
		if resourceSpans == nil {
			continue
		}
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			if scopeSpans == nil {
				continue
			}
			spanCount += len(scopeSpans.Spans)
			if spanCount > maxTraceSpans {
				return nil, fmt.Errorf("trace contains more than the supported limit of %d spans", maxTraceSpans)
			}
		}
	}
	if spanCount == 0 {
		return nil, fmt.Errorf("tempo returned a trace with no spans")
	}

	spans := make([]TraceSpan, 0, spanCount)
	for _, resourceSpans := range trace.ResourceSpans {
		if resourceSpans == nil {
			continue
		}
		resourceAttributes := attributesToMap(nil)
		if resourceSpans.Resource != nil {
			resourceAttributes = attributesToMap(resourceSpans.Resource.Attributes)
		}
		serviceName := attributeString(resourceAttributes, "service.name")

		for _, scopeSpans := range resourceSpans.ScopeSpans {
			if scopeSpans == nil {
				continue
			}
			for _, span := range scopeSpans.Spans {
				if span == nil {
					continue
				}
				attributes := make(map[string]any, len(resourceAttributes)+len(span.Attributes)+2)
				for key, value := range resourceAttributes {
					attributes["resource."+key] = value
				}
				for key, value := range attributesToMap(span.Attributes) {
					attributes[key] = value
				}
				if scopeSpans.Scope != nil {
					if scopeSpans.Scope.Name != "" {
						attributes["scope.name"] = scopeSpans.Scope.Name
					}
					if scopeSpans.Scope.Version != "" {
						attributes["scope.version"] = scopeSpans.Scope.Version
					}
				}

				events := normalizeEvents(span.Events)
				status := traceStatus(span.Status, events)
				var parentID *string
				if len(span.ParentSpanId) > 0 {
					value := fmt.Sprintf("%x", span.ParentSpanId)
					parentID = &value
				}
				durationNanos := span.EndTimeUnixNano - span.StartTimeUnixNano
				if span.EndTimeUnixNano < span.StartTimeUnixNano {
					durationNanos = 0
				}

				spans = append(spans, TraceSpan{
					ID:          fmt.Sprintf("%x", span.SpanId),
					ParentID:    parentID,
					Name:        span.Name,
					ServiceName: serviceName,
					StartTimeMS: float64(span.StartTimeUnixNano) / 1_000_000,
					DurationMS:  float64(durationNanos) / 1_000_000,
					Status:      status,
					Attributes:  attributes,
					Events:      events,
				})
			}
		}
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("tempo returned a trace with no spans")
	}
	return spans, nil
}

func normalizeEvents(events []*tracepb.Span_Event) []TraceEvent {
	result := make([]TraceEvent, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		result = append(result, TraceEvent{
			Name:       event.Name,
			TimeMS:     float64(event.TimeUnixNano) / 1_000_000,
			Attributes: attributesToMap(event.Attributes),
		})
	}
	return result
}

func traceStatus(status *tracepb.Status, events []TraceEvent) string {
	for _, event := range events {
		if event.Name == "exception" {
			return "error"
		}
		if _, ok := event.Attributes["exception.type"]; ok {
			return "error"
		}
	}
	if status != nil {
		switch status.Code {
		case tracepb.Status_STATUS_CODE_ERROR:
			return "error"
		case tracepb.Status_STATUS_CODE_OK:
			return "ok"
		}
	}
	return "unset"
}

func attributesToMap(attributes []*commonpb.KeyValue) map[string]any {
	result := make(map[string]any, len(attributes))
	for _, attribute := range attributes {
		if attribute == nil {
			continue
		}
		result[attribute.Key] = anyValue(attribute.Value)
	}
	return result
}

func anyValue(value *commonpb.AnyValue) any {
	if value == nil {
		return nil
	}
	switch v := value.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_BoolValue:
		return v.BoolValue
	case *commonpb.AnyValue_IntValue:
		if v.IntValue > 1<<53-1 || v.IntValue < -(1<<53-1) {
			return strconv.FormatInt(v.IntValue, 10)
		}
		return v.IntValue
	case *commonpb.AnyValue_DoubleValue:
		if math.IsNaN(v.DoubleValue) || math.IsInf(v.DoubleValue, 0) {
			return strconv.FormatFloat(v.DoubleValue, 'g', -1, 64)
		}
		return v.DoubleValue
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(v.BytesValue)
	case *commonpb.AnyValue_ArrayValue:
		if v.ArrayValue == nil {
			return []any{}
		}
		values := make([]any, 0, len(v.ArrayValue.Values))
		for _, item := range v.ArrayValue.Values {
			values = append(values, anyValue(item))
		}
		return values
	case *commonpb.AnyValue_KvlistValue:
		if v.KvlistValue == nil {
			return map[string]any{}
		}
		return attributesToMap(v.KvlistValue.Values)
	default:
		return nil
	}
}

func attributeString(attributes map[string]any, key string) string {
	value, ok := attributes[key]
	if !ok {
		return ""
	}
	result, _ := value.(string)
	return result
}

func validateTraceResultSize(result RenderTraceResult) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode trace result: %w", err)
	}
	if len(encoded) > maxTraceResultBytes {
		return fmt.Errorf(
			"trace result is %d bytes, exceeding the supported limit of %d bytes; no span or exception data was truncated",
			len(encoded),
			maxTraceResultBytes,
		)
	}
	return nil
}

func traceExploreURL(grafanaURL, grafanaVersion string, args RenderTraceParams) (string, error) {
	u, err := url.Parse(grafanaURL)
	if err != nil {
		return "", fmt.Errorf("parse Grafana URL for trace link: %w", err)
	}
	u.User = nil
	u.Path = path.Join(u.Path, "explore")
	u.RawPath = ""
	u.Fragment = ""

	query := map[string]any{
		"refId":     "A",
		"query":     args.TraceID,
		"queryType": "traceId",
	}
	if args.FocusSpanID != nil {
		query["spanId"] = *args.FocusSpanID
	}
	deeplinkArgs := GenerateDeeplinkParams{
		DatasourceUID: &args.DatasourceUID,
		Queries:       []map[string]any{query},
		TimeRange:     &TimeRange{From: "now-1h", To: "now"},
	}
	buildParams := buildExploreLeftParams
	if supportsExplorePanes(grafanaVersion) {
		buildParams = buildExplorePanesParams
	}
	values, err := buildParams(deeplinkArgs)
	if err != nil {
		return "", fmt.Errorf("encode Grafana trace link: %w", err)
	}
	u.RawQuery = values.Encode()
	return u.String(), nil
}
