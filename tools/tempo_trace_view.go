package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	maxTraceSpans       = 100_000
	maxTraceResultBytes = 1 << 20
)

// TraceViewResult is the structured content consumed by the trace MCP App.
type TraceViewResult struct {
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

// enrichTempoTrace adds an interactive view without changing the raw tool result.
func enrichTempoTrace(ctx context.Context, args GetTempoTraceParams, body string, raw *mcp.CallToolResult) {
	spans, err := decodeDisplayTrace(body)
	if err != nil {
		return
	}
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	grafanaURL := ""
	if deeplinkResolvesInRenderOrg(ctx, config.OrgID) {
		if publicURL, urlErr := grafanaBaseURLFromContext(ctx); urlErr == nil {
			grafanaURL, _ = traceExploreURL(publicURL, mcpgrafana.GrafanaVersion(ctx), args, spans)
		}
	}
	result := TraceViewResult{TraceID: args.TraceID, DatasourceUID: args.DatasourceUID, FocusSpanID: args.FocusSpanID, GrafanaURL: grafanaURL, Spans: spans}
	if validateTraceResultSize(result) == nil {
		raw.StructuredContent = result
	}
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

func decodeTraceResponse(reader io.Reader, maxBytes int64) (*tracepb.TracesData, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Tempo response: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("tempo response exceeds the supported limit of %d bytes; no span or exception data was truncated", maxBytes)
	}

	var envelope struct {
		Trace   json.RawMessage `json:"trace"`
		Status  string          `json:"status"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode Tempo response: %w", err)
	}
	if strings.EqualFold(envelope.Status, "partial") {
		return nil, fmt.Errorf("tempo returned a partial trace: %s", envelope.Message)
	}
	if len(envelope.Trace) == 0 || string(envelope.Trace) == "null" {
		return nil, fmt.Errorf("tempo response did not contain a trace")
	}
	traceData := &tracepb.TracesData{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(envelope.Trace, traceData); err != nil {
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
	size := 0
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
				if len(span.SpanId) != 8 || (len(span.ParentSpanId) != 0 && len(span.ParentSpanId) != 8) {
					return nil, fmt.Errorf("invalid OTLP span ID")
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

				normalized := TraceSpan{
					ID:          fmt.Sprintf("%x", span.SpanId),
					ParentID:    parentID,
					Name:        span.Name,
					ServiceName: serviceName,
					StartTimeMS: float64(span.StartTimeUnixNano) / 1_000_000,
					DurationMS:  float64(durationNanos) / 1_000_000,
					Status:      status,
					Attributes:  attributes,
					Events:      events,
				}
				if err := addTraceSpanSize(&size, normalized); err != nil {
					return nil, err
				}
				spans = append(spans, normalized)
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
	if status != nil {
		switch status.Code {
		case tracepb.Status_STATUS_CODE_ERROR:
			return "error"
		case tracepb.Status_STATUS_CODE_OK:
			return "ok"
		}
	}
	for _, event := range events {
		if event.Name == "exception" {
			return "error"
		}
		if _, ok := event.Attributes["exception.type"]; ok {
			return "error"
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

func addTraceSpanSize(size *int, span TraceSpan) error {
	encoded, err := json.Marshal(span)
	if err != nil {
		return fmt.Errorf("encode trace span: %w", err)
	}
	*size += len(encoded) + 1
	if *size > maxTraceResultBytes {
		return fmt.Errorf("trace exceeds structured-content limit")
	}
	return nil
}

func validateTraceResultSize(result TraceViewResult) error {
	// Measure each span separately so repeated resource attributes cannot create
	// an unbounded allocation while checking the structured-content limit.
	spans := result.Spans
	result.Spans = []TraceSpan{}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode trace result: %w", err)
	}
	size := len(encoded)
	for i, span := range spans {
		encoded, err = json.Marshal(span)
		if err != nil {
			return fmt.Errorf("encode trace span: %w", err)
		}
		size += len(encoded)
		if i > 0 {
			size++
		}
		if size > maxTraceResultBytes {
			return fmt.Errorf("trace result exceeds the supported limit of %d bytes; no span or exception data was truncated", maxTraceResultBytes)
		}
	}
	return nil
}

func traceExploreURL(grafanaURL, grafanaVersion string, args GetTempoTraceParams, spans []TraceSpan) (string, error) {
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
	var timeRange *TimeRange
	if len(spans) > 0 {
		start, end := spans[0].StartTimeMS, spans[0].StartTimeMS+spans[0].DurationMS
		for _, span := range spans[1:] {
			start = math.Min(start, span.StartTimeMS)
			end = math.Max(end, span.StartTimeMS+span.DurationMS)
		}
		// Leave room on both sides for clock skew and Grafana's time-range filtering.
		timeRange = &TimeRange{
			From: strconv.FormatInt(time.UnixMilli(int64(start)).Add(-time.Minute).UnixMilli(), 10),
			To:   strconv.FormatInt(time.UnixMilli(int64(math.Ceil(end))).Add(time.Minute).UnixMilli(), 10),
		}
	}
	deeplinkArgs := GenerateDeeplinkParams{
		DatasourceUID: &args.DatasourceUID,
		Queries:       []map[string]any{query},
		TimeRange:     timeRange,
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
