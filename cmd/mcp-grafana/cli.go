package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
	mcptools "github.com/grafana/mcp-grafana/tools"
)

const (
	exitOK            = 0
	exitToolError     = 1 // tool returned IsError=true
	exitInternalError = 2 // usage error, unknown tool, bad JSON, handler failure
)

type cliContextProvider func() context.Context

type cliCommand struct {
	tool    mcpgrafana.Tool
	request mcp.CallToolRequest
}

// parseCLICommand parses CLI args into a command to execute.
// Returns nil command and an exit code if the args were handled (help, error).
func parseCLICommand(args []string, stdin io.Reader, tools map[string]mcpgrafana.Tool, stdout, stderr io.Writer) (*cliCommand, int) {
	if len(args) == 0 {
		printTopLevelHelp(tools, stdout)
		return nil, exitOK
	}

	toolName := args[0]
	toolArgs := args[1:]

	tool, ok := tools[toolName]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "Error: unknown tool %q\n", toolName)
		suggestions := findSimilarTools(toolName, tools)
		if len(suggestions) > 0 {
			_, _ = fmt.Fprintf(stderr, "Did you mean: %s?\n", strings.Join(suggestions, ", "))
		}
		return nil, exitInternalError
	}

	if len(toolArgs) > 0 && (toolArgs[0] == "--help" || toolArgs[0] == "-h") {
		printToolHelp(tool, stdout)
		return nil, exitOK
	}

	var jsonInput []byte
	if len(toolArgs) > 0 {
		jsonInput = []byte(toolArgs[0])
	} else if stdin != nil {
		var err error
		jsonInput, err = io.ReadAll(stdin)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: failed to read stdin: %v\n", err)
			return nil, exitInternalError
		}
	}

	if len(jsonInput) == 0 || strings.TrimSpace(string(jsonInput)) == "" {
		jsonInput = []byte("{}")
	}

	var arguments map[string]any
	if err := json.Unmarshal(jsonInput, &arguments); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: invalid JSON input: %v\n", err)
		return nil, exitInternalError
	}

	request := mcp.CallToolRequest{}
	request.Params.Name = toolName
	request.Params.Arguments = arguments

	return &cliCommand{tool: tool, request: request}, exitOK
}

func executeCLI(ctxProvider cliContextProvider, registry *mcpgrafana.ToolCollector, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if ctxProvider == nil {
		ctxProvider = context.Background
	}

	cmd, code := parseCLICommand(args, stdin, registry.Tools(), stdout, stderr)
	if cmd == nil {
		return code
	}

	ctx := ctxProvider()
	result, err := cmd.tool.Handler(ctx, cmd.request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return exitInternalError
	}

	if result == nil {
		_, _ = fmt.Fprintln(stdout, "{}")
		return exitOK
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: failed to encode JSON output: %v\n", err)
		return exitInternalError
	}

	if result.IsError {
		return exitToolError
	}
	return exitOK
}

// printTopLevelHelp lists all available tools with descriptions.
func printTopLevelHelp(tools map[string]mcpgrafana.Tool, w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: mcp-grafana cli [--help] <tool-name> [--help] [json-params]")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Environment variables:")
	_, _ = fmt.Fprintln(w, "  GRAFANA_URL                       Grafana instance URL")
	_, _ = fmt.Fprintln(w, "  GRAFANA_SERVICE_ACCOUNT_TOKEN      Service account token for authentication")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Available tools:")
	_, _ = fmt.Fprintln(w)

	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)

	maxLen := 0
	for _, name := range names {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	for _, name := range names {
		tool := tools[name]
		desc := strings.TrimSpace(tool.Tool.Description)
		if i := strings.Index(desc, ". "); i != -1 {
			desc = desc[:i+1]
		}
		_, _ = fmt.Fprintf(w, "  %-*s  %s\n", maxLen, name, desc)
	}
}

// printToolHelp shows the parameter schema for a specific tool.
func printToolHelp(tool mcpgrafana.Tool, w io.Writer) {
	_, _ = fmt.Fprintf(w, "Tool: %s\n", tool.Tool.Name)
	if tool.Tool.Description != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", tool.Tool.Description)
	}
	_, _ = fmt.Fprintln(w)

	if len(tool.Tool.RawInputSchema) == 0 {
		_, _ = fmt.Fprintln(w, "No parameters.")
		return
	}

	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tool.Tool.RawInputSchema, &schema); err != nil {
		_, _ = fmt.Fprintf(w, "Parameters (raw JSON schema):\n%s\n", string(tool.Tool.RawInputSchema))
		return
	}

	if len(schema.Properties) == 0 {
		_, _ = fmt.Fprintln(w, "No parameters.")
		return
	}

	requiredSet := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		requiredSet[r] = true
	}

	paramNames := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		paramNames = append(paramNames, name)
	}
	sort.Strings(paramNames)

	_, _ = fmt.Fprintln(w, "Parameters:")
	for _, name := range paramNames {
		prop := schema.Properties[name]
		req := ""
		if requiredSet[name] {
			req = " (required)"
		}
		_, _ = fmt.Fprintf(w, "  %s (%s)%s\n", name, prop.Type, req)
		if prop.Description != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", prop.Description)
		}
	}
}

// findSimilarTools returns tool names that are similar to the given name.
// Uses simple prefix/substring matching.
func findSimilarTools(name string, tools map[string]mcpgrafana.Tool) []string {
	var suggestions []string
	lower := strings.ToLower(name)
	for toolName := range tools {
		toolLower := strings.ToLower(toolName)
		if strings.Contains(toolLower, lower) {
			suggestions = append(suggestions, toolName)
		}
	}
	sort.Strings(suggestions)
	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}
	return suggestions
}

// runCLI is the entry point called from main when "cli" subcommand is detected.
func runCLI(args []string) int {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)

	var gc grafanaConfig
	gc.addFlags(fs)

	var enabledTools string
	var disableWrite bool
	fs.StringVar(&enabledTools, "enabled-tools", defaultEnabledTools, "Comma-separated list of tool categories to enable")
	fs.BoolVar(&disableWrite, "disable-write", false, "Disable write tools (create/update operations)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitInternalError
	}

	registry := mcpgrafana.NewToolCollector()
	mcptools.CollectAllTools(registry, strings.Split(enabledTools, ","), !disableWrite)

	// Build execution context lazily so help/unknown-tool paths do not depend on env validity.
	cfg := gc.toGrafanaConfig()
	ctxProvider := func() context.Context {
		cf := mcpgrafana.ComposedStdioContextFunc(cfg)
		return cf(context.Background())
	}

	// Only read from stdin if it's piped (not a terminal).
	var stdin io.Reader
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		stdin = os.Stdin
	}

	return executeCLI(ctxProvider, registry, fs.Args(), stdin, os.Stdout, os.Stderr)
}
