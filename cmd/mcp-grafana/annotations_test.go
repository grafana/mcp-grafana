package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// listAllTools registers every tool category on a fresh server (with write
// tools enabled or disabled) and returns the tools exactly as a client would
// see them from tools/list.
func listAllTools(t *testing.T, disableWrite bool) []*mcp.Tool {
	t.Helper()

	dt := disabledTools{write: disableWrite}
	var categories []string
	for _, e := range dt.toolEntries() {
		categories = append(categories, e.category)
	}
	dt.enabledTools = strings.Join(categories, ",")

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	dt.processTools(srv)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.Tools)
	return result.Tools
}

// TestAllToolsDeclareAnnotationHints asserts that every tool exposed by the
// server explicitly sets the three MCP tool annotations readOnlyHint,
// destructiveHint and openWorldHint.
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
				if ann == nil {
					violations = append(violations, fmt.Sprintf("%s: missing annotations", tool.Name))
					continue
				}
				var missing []string
				// go-sdk limitation: ReadOnlyHint and IdempotentHint are bool,
				// so zero-value false is indistinguishable from "not set".
				// We rely on the ann != nil check above (every tool must call
				// at least one With*Annotation) and the conflict check below
				// to catch mis-labelled tools. DestructiveHint and OpenWorldHint
				// are *bool, so nil detection still works for those.
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
				if ann.ReadOnlyHint && *ann.DestructiveHint {
					violations = append(violations, fmt.Sprintf("%s: readOnlyHint=true conflicts with destructiveHint=true", tool.Name))
				}
			}
			if len(violations) > 0 {
				t.Errorf("tools with missing or inconsistent annotations (%d):\n%s", len(violations), strings.Join(violations, "\n"))
			}
		})
	}
}
