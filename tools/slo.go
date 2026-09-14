package tools

import (
	"context"
	"fmt"

	sloclient "github.com/grafana/gcx/client/slo"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ListSLOsParams struct {
	Limit int `json:"limit" jsonschema:"default=50,description=The maximum number of SLO definitions to return"`
}

type sloSummary struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Objectives  []string `json:"objectives,omitempty"`
	Status      string   `json:"status,omitempty"`
}

func summarizeSLOs(slos []sloclient.Slo) []sloSummary {
	result := make([]sloSummary, 0, len(slos))
	for _, s := range slos {
		summary := sloSummary{
			UUID:        s.UUID,
			Name:        s.Name,
			Description: s.Description,
		}
		for _, o := range s.Objectives {
			summary.Objectives = append(summary.Objectives, fmt.Sprintf("%.3f%% over %s", o.Value*100, o.Window))
		}
		if s.ReadOnly != nil && s.ReadOnly.Status != nil {
			summary.Status = s.ReadOnly.Status.Type
		}
		result = append(result, summary)
	}
	return result
}

type ListSLOsResult struct {
	SLOs    []sloSummary `json:"slos"`
	HasMore bool         `json:"hasMore"`
}

func listSLOs(ctx context.Context, args ListSLOsParams) (*ListSLOsResult, error) {
	c := mcpgrafana.SLOClientFromContext(ctx)
	if c == nil {
		return nil, fmt.Errorf("list SLOs: no SLO client configured")
	}

	slos, err := c.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list SLOs: %w", err)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	hasMore := len(slos) > limit
	if hasMore {
		slos = slos[:limit]
	}

	return &ListSLOsResult{
		SLOs:    summarizeSLOs(slos),
		HasMore: hasMore,
	}, nil
}

var ListSLOs = mcpgrafana.MustTool(
	"list_slos",
	"List Grafana SLO (Service Level Objective) definitions. Returns each SLO's name, description, objectives (target and time window), and current status.",
	listSLOs,
	mcp.WithTitleAnnotation("List SLOs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// AddSLOTools registers the SLO tools backed by gcx's client/slo library.
func AddSLOTools(mcp *server.MCPServer) {
	ListSLOs.Register(mcp)
}
