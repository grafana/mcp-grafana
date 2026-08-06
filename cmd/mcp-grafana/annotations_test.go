package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// listAllTools registers every tool category on a fresh server (with write
// tools enabled or disabled) and returns the tools exactly as a client would
// see them from tools/list.
func listAllTools(t *testing.T, disableWrite bool) []mcp.Tool {
	t.Helper()

	dt := disabledTools{write: disableWrite}
	var categories []string
	for _, e := range dt.toolEntries() {
		categories = append(categories, e.category)
	}
	dt.enabledTools = strings.Join(categories, ",")

	srv := server.NewMCPServer("test", "0")
	dt.processTools(srv)

	response := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	raw, err := json.Marshal(response)
	require.NoError(t, err)

	var parsed struct {
		Result mcp.ListToolsResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.Nil(t, parsed.Error, "tools/list returned an error")
	require.NotEmpty(t, parsed.Result.Tools)
	return parsed.Result.Tools
}

// TestAllToolsDeclareAnnotationHints asserts that every tool exposed by the
// server explicitly sets the three MCP tool annotations readOnlyHint,
// destructiveHint and openWorldHint. Directory listings (e.g. the OpenAI
// plugin directory) fail closed on any omission, so a tool missing any of the
// three would silently delist the hosted server. See
// https://github.com/grafana/mcp-grafana/issues/1009.
func TestAllToolsDeclareAnnotationHints(t *testing.T) {
	for _, disableWrite := range []bool{false, true} {
		name := "write-enabled"
		if disableWrite {
			name = "write-disabled"
		}
		t.Run(name, func(t *testing.T) {
			var violations []string
			for _, tool := range listAllTools(t, disableWrite) {
				ann := tool.Annotations
				var missing []string
				if ann.ReadOnlyHint == nil {
					missing = append(missing, "readOnlyHint")
				}
				if ann.DestructiveHint == nil {
					missing = append(missing, "destructiveHint")
				}
				if ann.OpenWorldHint == nil {
					missing = append(missing, "openWorldHint")
				}
				if len(missing) > 0 {
					violations = append(violations, fmt.Sprintf("%s: missing %s", tool.Name, strings.Join(missing, ", ")))
					continue
				}
				// destructiveHint is only meaningful for write tools; a
				// read-only tool claiming to be destructive indicates a
				// mislabelled annotation on one side or the other.
				if *ann.ReadOnlyHint && *ann.DestructiveHint {
					violations = append(violations, fmt.Sprintf("%s: readOnlyHint=true conflicts with destructiveHint=true", tool.Name))
				}
			}
			if len(violations) > 0 {
				t.Errorf("tools with missing or inconsistent annotations (%d):\n%s", len(violations), strings.Join(violations, "\n"))
			}
		})
	}
}
