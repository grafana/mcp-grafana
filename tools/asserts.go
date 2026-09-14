package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	kgclient "github.com/grafana/gcx/client/kg"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func newAssertsClient(ctx context.Context) (*kgclient.Client, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	return kgclient.NewClient(&http.Client{Transport: transport}, cfg.URL), nil
}

type GetAssertionsParams struct {
	StartTime  string `json:"startTime" jsonschema:"required,description=The start time in RFC3339 format (e.g. 2024-01-01T00:00:00Z) or relative format (e.g. now-1h)"`
	EndTime    string `json:"endTime" jsonschema:"required,description=The end time in RFC3339 format (e.g. 2024-01-01T00:00:00Z) or relative format (e.g. now)"`
	EntityType string `json:"entityType" jsonschema:"description=The type of the entity to list (e.g. Service\\, Node\\, Pod\\, etc.)"`
	EntityName string `json:"entityName" jsonschema:"description=The name of the entity to list"`
	Env        string `json:"env,omitempty" jsonschema:"description=The env of the entity to list"`
	Site       string `json:"site,omitempty" jsonschema:"description=The site of the entity to list"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"description=The namespace of the entity to list"`
}

func getAssertions(ctx context.Context, args GetAssertionsParams) (string, error) {
	startTime, err := parseStartTime(args.StartTime)
	if err != nil {
		return "", fmt.Errorf("parsing start time: %w", err)
	}
	endTime, err := parseEndTime(args.EndTime)
	if err != nil {
		return "", fmt.Errorf("parsing end time: %w", err)
	}

	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	entityScope := map[string]any{}
	if args.Env != "" {
		entityScope["env"] = args.Env
	}
	if args.Site != "" {
		entityScope["site"] = args.Site
	}
	if args.Namespace != "" {
		entityScope["namespace"] = args.Namespace
	}

	summary, err := client.LLMSummary(ctx, kgclient.LLMSummaryRequest{
		StartTime: startTime.UnixMilli(),
		EndTime:   endTime.UnixMilli(),
		EntityKeys: []kgclient.EntityKey{
			{
				Name:  args.EntityName,
				Type:  args.EntityType,
				Scope: entityScope,
			},
		},
		SuggestionSrcEntities: []kgclient.EntityKey{},
		AlertCategories:       []string{"saturation", "amend", "anomaly", "failure", "error"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch data: %w", err)
	}

	data, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response body: %w", err)
	}

	return string(data), nil
}

var GetAssertions = mcpgrafana.MustTool(
	"get_assertions",
	"Get assertion summary for a given entity with its type, name, env, site, namespace, and a time range",
	getAssertions,
	mcp.WithTitleAnnotation("Get assertions summary"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

func AddAssertsTools(mcp *server.MCPServer) {
	GetAssertions.Register(mcp)
}
