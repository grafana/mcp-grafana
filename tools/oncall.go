package tools

import (
	"context"
	"fmt"
	"strings"

	aapi "github.com/grafana/amixr-api-go-client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

const (
	SCHEDULES_ENDPOINT = "schedules/"
)

func oncallClientFromContext(ctx context.Context) (*aapi.Client, error) {
	// Get the standard Grafana URL and API key
	grafanaURL := mcpgrafana.GrafanaURLFromContext(ctx)

	// Try to get OnCall specific URL and API key
	grafanaOnCallURL := mcpgrafana.GrafanaOnCallURLFromContext(ctx)
	grafanaOnCallToken := mcpgrafana.GrafanaOnCallTokenFromContext(ctx)

	if grafanaOnCallURL == "" {
		return nil, fmt.Errorf("OnCall API URL is not set")
	}

	if grafanaOnCallToken == "" {
		return nil, fmt.Errorf("OnCall Token is not set")
	}

	// Make sure the URL doesn't end with a slash as the client will append paths
	grafanaOnCallURL = strings.TrimRight(grafanaOnCallURL, "/")

	// Create a new OnCall client
	client, err := aapi.NewWithGrafanaURL(grafanaOnCallURL, grafanaOnCallToken, grafanaURL)
	if err != nil {
		return nil, fmt.Errorf("creating OnCall client: %w", err)
	}

	return client, nil
}

type ListOnCallSchedulesParams struct {
	TeamID string `json:"teamId,omitempty" jsonschema:"description=The ID of the team to list schedules for"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=The maximum number of schedules to return"`
}

func listOnCallSchedules(ctx context.Context, args ListOnCallSchedulesParams) ([]*aapi.Schedule, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	listOptions := &aapi.ListScheduleOptions{}

	if args.Limit > 0 {
		listOptions.ListOptions.Page = args.Limit
	}

	scheduleService := aapi.NewScheduleService(client)
	response, _, err := scheduleService.ListSchedules(listOptions)
	if err != nil {
		return nil, fmt.Errorf("listing OnCall schedules: %w", err)
	}

	// Filter by team ID if provided
	if args.TeamID != "" {
		filteredSchedules := []*aapi.Schedule{}
		for _, schedule := range response.Schedules {
			if schedule.TeamId == args.TeamID {
				filteredSchedules = append(filteredSchedules, schedule)
			}
		}
		return filteredSchedules, nil
	}

	return response.Schedules, nil
}

var ListOnCallSchedules = mcpgrafana.MustTool(
	"list_oncall_schedules",
	"List schedules from Grafana OnCall",
	listOnCallSchedules,
)

func AddOnCallTools(mcp *server.MCPServer) {
	ListOnCallSchedules.Register(mcp)
}
