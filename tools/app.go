package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/invopop/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type renderAppParams struct {
	ToolName  string         `json:"toolName" jsonschema:"required,description=The query tool whose result to display"`
	Arguments map[string]any `json:"arguments" jsonschema:"required,description=Inputs for the selected query tool: the schema whose title is toolName"`
}

const (
	maxAppNotes     = 5
	maxAppNoteRunes = 200
)

// appNoter is implemented by a source's structured content to tell the model
// facts it cannot read from the view, such as a failed query.
type appNoter interface {
	AppNotes() []string
}

func appText(toolName string, content any) string {
	lines := []string{fmt.Sprintf("Displayed %s result in the Grafana App.", toolName)}
	noter, ok := content.(appNoter)
	if !ok {
		return lines[0]
	}
	for _, note := range noter.AppNotes() {
		note = strings.Join(strings.Fields(note), " ")
		if note == "" {
			continue
		}
		if runes := []rune(note); len(runes) > maxAppNoteRunes {
			note = string(runes[:maxAppNoteRunes-1]) + "…"
		}
		lines = append(lines, note)
		if len(lines) > maxAppNotes {
			break
		}
	}
	return strings.Join(lines, "\n")
}

// AddAppTool registers one explicit viewer for the supplied enabled query tools.
// Sources retain ownership of their input contracts, execution and permissions.
func AddAppTool(s *mcp.Server, sources ...mcpgrafana.Tool) {
	if len(sources) == 0 {
		return
	}
	var names []any
	var schemas []*jsonschema.Schema
	var permissions []string
	// A source whose queries can mutate data makes the viewer mutating too.
	readOnly := true
	for _, source := range sources {
		if a := source.Tool.Annotations; a == nil || !a.ReadOnlyHint {
			readOnly = false
		}
		names = append(names, source.Tool.Name)
		schema := new(jsonschema.Schema)
		if err := json.Unmarshal(source.Tool.InputSchema.(json.RawMessage), schema); err != nil {
			panic(fmt.Errorf("read app source schema: %w", err))
		}
		// The source is not in tools/list, so its branch carries its name and description.
		schema.Title = source.Tool.Name
		schema.Description = source.Tool.Description
		schemas = append(schemas, schema)
		required, _ := mcpgrafana.ToolRequiredPermissions(source.Tool)
		for _, permission := range required {
			if !slices.Contains(permissions, permission) {
				permissions = append(permissions, permission)
			}
		}
	}
	tool := mcpgrafana.MustTool(
		"render_grafana_app",
		"Run a query tool and display its result in the interactive Grafana App. Use for an explicit visualization request; call the query tool directly for analysis. Supply the selected tool's inputs in arguments. The view is derived from the returned data.",
		func(ctx context.Context, args renderAppParams) (*mcp.CallToolResult, error) {
			for _, source := range sources {
				if source.Tool.Name != args.ToolName {
					continue
				}
				if _, nested := args.Arguments["orgId"]; nested {
					return nil, fmt.Errorf("orgId belongs on render_grafana_app, outside arguments")
				}
				body, err := json.Marshal(args.Arguments)
				if err != nil {
					return nil, fmt.Errorf("marshal app query arguments: %w", err)
				}
				result, err := source.Handler(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
					Name: args.ToolName, Arguments: body,
				}})
				if err != nil || result.IsError {
					return result, err
				}
				if result.StructuredContent == nil {
					return nil, fmt.Errorf("result too large or unsupported for the interactive view; use %s directly", args.ToolName)
				}
				view := *result
				view.Content = []mcp.Content{&mcp.TextContent{Text: appText(args.ToolName, result.StructuredContent)}}
				return &view, nil
			}
			return nil, fmt.Errorf("tool %q is not an enabled app source", args.ToolName)
		},
		func(tool *mcp.Tool) {
			var schema jsonschema.Schema
			if err := json.Unmarshal(tool.InputSchema.(json.RawMessage), &schema); err != nil {
				panic(err)
			}
			toolName, _ := schema.Properties.Get("toolName")
			toolName.Enum = names
			arguments, _ := schema.Properties.Get("arguments")
			arguments.AnyOf = schemas
			body, err := json.Marshal(schema)
			if err != nil {
				panic(err)
			}
			tool.InputSchema = json.RawMessage(body)
		},
		mcpgrafana.WithUIResource(mcpgrafana.GrafanaAppResourceURI),
		mcpgrafana.WithTitleAnnotation("Show Grafana App"),
		mcpgrafana.WithIdempotentHintAnnotation(true),
		mcpgrafana.WithReadOnlyHintAnnotation(readOnly),
		mcpgrafana.WithDestructiveHintAnnotation(!readOnly),
		mcpgrafana.WithOpenWorldHintAnnotation(false),
		mcpgrafana.RequiresPermissions(permissions...),
	)
	tool.Register(s)
}
