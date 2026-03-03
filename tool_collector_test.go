package mcpgrafana

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestToolCollectorAddTool(t *testing.T) {
	c := NewToolCollector()

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}

	tool := mcp.Tool{Name: "test_tool", Description: "A test tool"}
	c.AddTool(tool, handler)

	tools := c.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	got, ok := tools["test_tool"]
	if !ok {
		t.Fatal("expected tool 'test_tool' to be present")
	}
	if got.Tool.Name != "test_tool" {
		t.Errorf("expected tool name 'test_tool', got %q", got.Tool.Name)
	}
	if got.Tool.Description != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", got.Tool.Description)
	}
	if got.Handler == nil {
		t.Error("expected handler to be non-nil")
	}
}

func TestToolCollectorMultipleTools(t *testing.T) {
	c := NewToolCollector()

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, nil
	}

	c.AddTool(mcp.Tool{Name: "tool_a"}, handler)
	c.AddTool(mcp.Tool{Name: "tool_b"}, handler)
	c.AddTool(mcp.Tool{Name: "tool_c"}, handler)

	tools := c.Tools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
	for _, name := range []string{"tool_a", "tool_b", "tool_c"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected tool %q to be present", name)
		}
	}
}

func TestToolCollectorOverwrite(t *testing.T) {
	c := NewToolCollector()

	handler1 := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("first"), nil
	}
	handler2 := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("second"), nil
	}

	c.AddTool(mcp.Tool{Name: "dupe", Description: "first"}, handler1)
	c.AddTool(mcp.Tool{Name: "dupe", Description: "second"}, handler2)

	tools := c.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool after overwrite, got %d", len(tools))
	}
	if tools["dupe"].Tool.Description != "second" {
		t.Errorf("expected overwritten description 'second', got %q", tools["dupe"].Tool.Description)
	}
}

// Compile-time check that ToolCollector satisfies ToolAdder.
var _ ToolAdder = (*ToolCollector)(nil)

// Compile-time check that *server.MCPServer satisfies ToolAdder.
var _ ToolAdder = (*server.MCPServer)(nil)
