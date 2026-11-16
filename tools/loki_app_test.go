package tools

import (
	"context"
	"encoding/json"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestLogsAppCompactOrderAndFields(t *testing.T) {
	result := &QueryLokiLogsResult{Data: []LogEntry{}, Streams: []CompactStream{
		{Labels: map[string]string{"app": "a"}, Parsed: map[string]string{"level": "info"}, Lines: []CompactLine{
			{Timestamp: "2026-11-13T10:00:00.000000001Z", Line: "first"},
			{Timestamp: "2026-11-13T10:00:00.000000003Z", Line: "third", Parsed: map[string]string{"level": "error"}},
		}},
		{Labels: map[string]string{"app": "b"}, Lines: []CompactLine{{Timestamp: "2026-11-13T10:00:00.000000002Z", Line: ""}}},
	}}
	before, err := json.Marshal(result)
	require.NoError(t, err)
	for _, direction := range []string{"forward", "backward"} {
		raw, err := newLogsAppResult(result, QueryLokiLogsParams{LogQL: `{app=~"a|b"}`, Direction: direction}, "https://example.org/explore")
		require.NoError(t, err)
		require.Equal(t, string(before), raw.Content[0].(*mcp.TextContent).Text)
		view := raw.StructuredContent.(logsView)
		require.Nil(t, view.Logs.Streams)
		require.Len(t, view.Logs.Data, 3)
		require.Equal(t, "", view.Logs.Data[1].Line)
		require.Equal(t, "2026-11-13T10:00:00.000000002Z", view.Logs.Data[1].Timestamp)
		first, last := "first", "third"
		if direction == "backward" {
			first, last = last, first
		}
		require.Equal(t, first, view.Logs.Data[0].Line)
		require.Equal(t, last, view.Logs.Data[2].Line)
		for _, entry := range view.Logs.Data {
			if entry.Line == "third" {
				require.Equal(t, "error", entry.Parsed["level"])
			}
			if entry.Line == "first" {
				require.Equal(t, "info", entry.Parsed["level"])
			}
		}
	}
	after, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.Equal(t, "info", result.Streams[0].Parsed["level"])
}

func TestLogsAppFullAndCompactMatch(t *testing.T) {
	line := LogEntry{Timestamp: "2026-11-13T10:00:00Z", Line: "message", Labels: map[string]string{"app": "api"}, StructuredMetadata: map[string]string{"trace_id": "abc"}}
	full := &QueryLokiLogsResult{Data: []LogEntry{line}}
	compact := &QueryLokiLogsResult{Data: []LogEntry{}, Streams: compactLogEntries(full.Data)}
	a, err := newLogsAppResult(full, QueryLokiLogsParams{}, "")
	require.NoError(t, err)
	b, err := newLogsAppResult(compact, QueryLokiLogsParams{}, "")
	require.NoError(t, err)
	require.Equal(t, a.StructuredContent, b.StructuredContent)
}

func TestLogsAppBudgetKeepsSourceText(t *testing.T) {
	result := &QueryLokiLogsResult{Data: []LogEntry{{Line: strings.Repeat("x", maxLogsViewBytes), Labels: map[string]string{}}}}
	raw, err := newLogsAppResult(result, QueryLokiLogsParams{}, "")
	require.NoError(t, err)
	require.Nil(t, raw.StructuredContent)
	require.Contains(t, raw.Content[0].(*mcp.TextContent).Text, result.Data[0].Line)
}

func TestLogsAppNotes(t *testing.T) {
	require.Contains(t, (logsView{Logs: QueryLokiLogsResult{Metadata: &QueryMetadata{ResultsTruncated: true}}}).AppNotes()[0], "truncated")
	require.Contains(t, (logsView{}).AppNotes()[0], "No logs")
	value := 3.0
	require.Contains(t, (logsView{Logs: QueryLokiLogsResult{Data: []LogEntry{{Value: &value}}}}).AppNotes()[0], "metric samples")
}

func TestLogsExploreURL(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "https://grafana.example.com/grafana"})
	ctx = mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{PublicURL: "https://grafana.example.com/grafana", Version: "9.5.0"})
	args := QueryLokiLogsParams{DatasourceUID: "loki", LogQL: `{app="api"} |= "failure"`}
	result := &QueryLokiLogsResult{Metadata: &QueryMetadata{StartTime: "2026-11-13T10:00:00Z", EndTime: "2026-11-13T11:00:00Z"}}
	link, err := url.Parse(logsExploreURL(ctx, args, result))
	require.NoError(t, err)
	require.Equal(t, "/grafana/explore", link.Path)
	// Older Grafana versions use the existing legacy Explore state builder.
	var state map[string]any
	require.NoError(t, json.Unmarshal([]byte(link.Query().Get("left")), &state))
	require.Equal(t, "loki", state["datasource"])
	query := state["queries"].([]any)[0].(map[string]any)
	require.Equal(t, args.LogQL, query["expr"])
	require.Equal(t, "backward", query["direction"])
	args.QueryType = "instant"
	require.Empty(t, logsExploreURL(ctx, args, result))
	args.QueryType = "range"
	require.Empty(t, logsExploreURL(ctx, args, &QueryLokiLogsResult{}))
}
