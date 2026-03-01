package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func newTestRegistry() *mcpgrafana.ToolCollector {
	c := mcpgrafana.NewToolCollector()

	echoSchema := json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"Message to echo"}},"required":["message"]}`)
	c.AddTool(
		mcp.Tool{Name: "echo_tool", Description: "Echoes the input message", RawInputSchema: echoSchema},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			msg, _ := req.GetArguments()["message"].(string)
			return mcp.NewToolResultText(msg), nil
		},
	)

	c.AddTool(
		mcp.Tool{Name: "soft_error_tool", Description: "Always returns a soft error", RawInputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "something went wrong"}},
				IsError: true,
			}, nil
		},
	)

	c.AddTool(
		mcp.Tool{Name: "hard_error_tool", Description: "Always returns a hard error", RawInputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, fmt.Errorf("fatal: connection refused")
		},
	)

	return c
}

func TestExecuteCLI(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		stdin    io.Reader
		wantCode int
		check    func(t *testing.T, stdout, stderr string)
	}{
		{
			name:     "no args shows help",
			args:     []string{},
			wantCode: exitOK,
			check: func(t *testing.T, stdout, stderr string) {
				if !strings.Contains(stdout, "echo_tool") {
					t.Error("expected no-args output to show tool listing")
				}
			},
		},
		{
			name:     "per-tool help",
			args:     []string{"echo_tool", "--help"},
			wantCode: exitOK,
			check: func(t *testing.T, stdout, stderr string) {
				if !strings.Contains(stdout, "echo_tool") {
					t.Error("expected per-tool help to contain tool name")
				}
				if !strings.Contains(stdout, "message") {
					t.Error("expected per-tool help to contain parameter name")
				}
				if !strings.Contains(stdout, "string") {
					t.Error("expected per-tool help to contain parameter type")
				}
			},
		},
		{
			name:     "positional JSON",
			args:     []string{"echo_tool", `{"message":"hello"}`},
			wantCode: exitOK,
			check: func(t *testing.T, stdout, stderr string) {
				var result cliResult
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("failed to parse JSON output: %v; raw: %s", err, stdout)
				}
				if len(result.Content) == 0 {
					t.Fatal("expected at least one content item")
				}
				if result.Content[0].Text != "hello" {
					t.Errorf("expected text 'hello', got %q", result.Content[0].Text)
				}
			},
		},
		{
			name:     "stdin JSON",
			args:     []string{"echo_tool"},
			stdin:    strings.NewReader(`{"message":"from stdin"}`),
			wantCode: exitOK,
			check: func(t *testing.T, stdout, stderr string) {
				var result cliResult
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("failed to parse JSON output: %v; raw: %s", err, stdout)
				}
				if len(result.Content) == 0 {
					t.Fatal("expected at least one content item")
				}
				if result.Content[0].Text != "from stdin" {
					t.Errorf("expected text 'from stdin', got %q", result.Content[0].Text)
				}
			},
		},
		{
			name:     "unknown tool",
			args:     []string{"nonexistent_tool"},
			wantCode: exitInternalError,
			check: func(t *testing.T, stdout, stderr string) {
				if !strings.Contains(stderr, "nonexistent_tool") {
					t.Error("expected error output to mention the unknown tool name")
				}
			},
		},
		{
			name:     "bad JSON",
			args:     []string{"echo_tool", `{invalid`},
			wantCode: exitInternalError,
			check: func(t *testing.T, stdout, stderr string) {
				if !strings.Contains(stderr, "invalid") || !strings.Contains(stderr, "JSON") {
					t.Errorf("expected error about invalid JSON, got: %s", stderr)
				}
			},
		},
		{
			name:     "soft error",
			args:     []string{"soft_error_tool", `{}`},
			wantCode: exitToolError,
			check: func(t *testing.T, stdout, stderr string) {
				var result cliResult
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("failed to parse JSON output: %v; raw: %s", err, stdout)
				}
				if !result.IsError {
					t.Error("expected IsError to be true for soft error")
				}
			},
		},
		{
			name:     "hard error",
			args:     []string{"hard_error_tool", `{}`},
			wantCode: exitInternalError,
			check: func(t *testing.T, stdout, stderr string) {
				if !strings.Contains(stderr, "connection refused") {
					t.Errorf("expected stderr to contain error message, got: %s", stderr)
				}
			},
		},
		{
			name:     "empty JSON input",
			args:     []string{"soft_error_tool", `{}`},
			wantCode: exitToolError,
		},
	}

	registry := newTestRegistry()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := executeCLI(context.Background, registry, tc.args, tc.stdin, &stdout, &stderr)

			if code != tc.wantCode {
				t.Fatalf("expected exit code %d, got %d; stdout: %s; stderr: %s", tc.wantCode, code, stdout.String(), stderr.String())
			}
			if tc.check != nil {
				tc.check(t, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCLIContextProviderInvocation(t *testing.T) {
	registry := newTestRegistry()

	tests := []struct {
		name             string
		args             []string
		wantCode         int
		wantContextCalls int
	}{
		{name: "no args", args: []string{}, wantCode: exitOK, wantContextCalls: 0},
		{name: "unknown tool", args: []string{"does_not_exist"}, wantCode: exitInternalError, wantContextCalls: 0},
		{name: "per tool help", args: []string{"echo_tool", "--help"}, wantCode: exitOK, wantContextCalls: 0},
		{name: "bad json", args: []string{"echo_tool", `{invalid`}, wantCode: exitInternalError, wantContextCalls: 0},
		{name: "tool execution", args: []string{"echo_tool", `{"message":"hello"}`}, wantCode: exitOK, wantContextCalls: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			called := 0
			ctxProvider := func() context.Context {
				called++
				return context.Background()
			}

			code := executeCLI(ctxProvider, registry, tc.args, nil, &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf("expected exit code %d, got %d", tc.wantCode, code)
			}
			if called != tc.wantContextCalls {
				t.Fatalf("expected context provider calls %d, got %d", tc.wantContextCalls, called)
			}
		})
	}
}

func TestRunCLINonExecutionPathsWithInvalidGrafanaURLDoNotPanic(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "help", args: []string{"--help"}, wantCode: exitOK},
		{name: "unknown tool", args: []string{"definitely_not_a_real_tool_name"}, wantCode: exitInternalError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRAFANA_URL", "http://[::1")

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("runCLI panicked for %q with invalid GRAFANA_URL: %v", tc.name, r)
				}
			}()

			code := runCLI(tc.args)
			if code != tc.wantCode {
				t.Fatalf("expected exit code %d, got %d", tc.wantCode, code)
			}
		})
	}
}

func TestFindSimilarTools(t *testing.T) {
	registry := make(map[string]mcpgrafana.Tool)
	for _, name := range []string{
		"search_dashboards",
		"search_folders",
		"search_alerts",
		"search_annotations",
		"search_users",
		"search_teams",
		"search_datasources",
		"get_dashboard",
		"list_alerts",
	} {
		registry[name] = mcpgrafana.Tool{Tool: mcp.Tool{Name: name}}
	}

	tests := []struct {
		name       string
		input      string
		wantMax    int
		wantSubset []string
	}{
		{
			name:    "prefix match capped at 5",
			input:   "search",
			wantMax: 5,
		},
		{
			name:       "substring match",
			input:      "alert",
			wantSubset: []string{"search_alerts", "list_alerts"},
		},
		{
			name:    "no match",
			input:   "nonexistent",
			wantMax: 0,
		},
		{
			name:       "case insensitive",
			input:      "DASHBOARD",
			wantSubset: []string{"search_dashboards", "get_dashboard"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findSimilarTools(tc.input, registry)
			if tc.wantMax > 0 && len(got) > tc.wantMax {
				t.Errorf("expected at most %d suggestions, got %d: %v", tc.wantMax, len(got), got)
			}
			if tc.wantMax == 0 && tc.wantSubset == nil && len(got) != 0 {
				t.Errorf("expected no suggestions, got %v", got)
			}
			for _, want := range tc.wantSubset {
				found := false
				for _, s := range got {
					if s == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected suggestion %q in %v", want, got)
				}
			}
			for i := 1; i < len(got); i++ {
				if got[i] < got[i-1] {
					t.Errorf("suggestions not sorted: %v", got)
					break
				}
			}
		})
	}
}

// cliResult is a simplified representation of the CLI JSON output
// for test assertions.
type cliResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}
