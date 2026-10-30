package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path"
	"strconv"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// setTraceViewContent attaches the trace viewer's structured content to raw
// when it fits the size limit.
func setTraceViewContent(ctx context.Context, args GetTempoTraceParams, spans []TraceSpan, raw *mcp.CallToolResult) {
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
