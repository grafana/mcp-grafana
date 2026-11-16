package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxLogsViewBytes = 1 << 20

// logsView keeps display context separate from the source query's JSON contract.
type logsView struct {
	Kind       string              `json:"kind"`
	Query      string              `json:"query"`
	ExploreURL string              `json:"exploreUrl,omitempty"`
	Logs       QueryLokiLogsResult `json:"logs"`
}

func (v logsView) AppNotes() []string {
	if v.Logs.Metadata != nil && v.Logs.Metadata.ResultsTruncated {
		return []string{"The returned logs reached the line limit; results are truncated."}
	}
	if len(v.Logs.Data) == 0 {
		return []string{"No logs matched this query."}
	}
	for _, entry := range v.Logs.Data {
		if entry.Value != nil || entry.Values != nil {
			return []string{"This query returned metric samples. Use a log query to show log lines."}
		}
	}
	return nil
}

func queryLokiLogsTool(ctx context.Context, args QueryLokiLogsParams) (*mcp.CallToolResult, error) {
	result, err := queryLokiLogs(ctx, args)
	if err != nil {
		return nil, err
	}
	return newLogsAppResult(result, args, logsExploreURL(ctx, args, result))
}

// Instant queries leave their effective range to the backend. Without resolved
// bounds, an Explore range could show a different window, so omit that link.
func logsExploreURL(ctx context.Context, args QueryLokiLogsParams, result *QueryLokiLogsResult) string {
	if args.QueryType == "instant" || result.Metadata == nil ||
		result.Metadata.StartTime == "" || result.Metadata.EndTime == "" ||
		!deeplinkResolvesInRenderOrg(ctx, mcpgrafana.GrafanaConfigFromContext(ctx).OrgID) {
		return ""
	}
	uid := args.DatasourceUID
	direction := args.Direction
	if direction == "" {
		direction = "backward"
	}
	link, _ := generateDeeplinkReadOnly(ctx, GenerateDeeplinkParams{
		ResourceType: "explore", DatasourceUID: &uid,
		Queries:   []map[string]any{{"refId": "A", "expr": args.LogQL, "queryType": "range", "direction": direction}},
		TimeRange: &TimeRange{From: result.Metadata.StartTime, To: result.Metadata.EndTime},
	})
	return link
}

func newLogsAppResult(result *QueryLokiLogsResult, args QueryLokiLogsParams, exploreURL string) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode Loki result: %w", err)
	}
	raw := mcpgrafana.NewToolResultText(string(body))
	if len(body) > maxLogsViewBytes {
		return raw, nil
	}
	// Expand compact streams only for display, preserving the original text result.
	logs := *result
	logs.Data = append([]LogEntry{}, result.Data...)
	for _, stream := range result.Streams {
		for _, line := range stream.Lines {
			logs.Data = append(logs.Data, LogEntry{
				Timestamp: line.Timestamp, Line: line.Line, Labels: stream.Labels,
				StructuredMetadata: mergeLogFields(stream.StructuredMetadata, line.StructuredMetadata),
				Parsed:             mergeLogFields(stream.Parsed, line.Parsed),
			})
		}
	}
	logs.Streams = nil
	for i := range logs.Data {
		if logs.Data[i].Labels == nil {
			logs.Data[i].Labels = map[string]string{}
		}
	}
	// Compact groups lose cross-stream ordering. time.Time compares nanoseconds
	// without JavaScript's millisecond rounding, and the original strings survive.
	sort.SliceStable(logs.Data, func(i, j int) bool {
		a, ae := time.Parse(time.RFC3339Nano, logs.Data[i].Timestamp)
		b, be := time.Parse(time.RFC3339Nano, logs.Data[j].Timestamp)
		if ae != nil || be != nil {
			return false
		}
		if args.Direction == "forward" {
			return a.Before(b)
		}
		return a.After(b)
	})
	view := logsView{Kind: "logs", Query: args.LogQL, ExploreURL: exploreURL, Logs: logs}
	payload, err := json.Marshal(view)
	if err != nil {
		return nil, fmt.Errorf("encode Loki view: %w", err)
	}
	if len(payload) <= maxLogsViewBytes {
		raw.StructuredContent = view
	}
	return raw, nil
}

func mergeLogFields(common, line map[string]string) map[string]string {
	if len(common)+len(line) == 0 {
		return nil
	}
	fields := make(map[string]string, len(common)+len(line))
	for key, value := range common {
		fields[key] = value
	}
	for key, value := range line {
		fields[key] = value
	}
	return fields
}
