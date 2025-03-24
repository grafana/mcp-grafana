package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	aapi "github.com/grafana/amixr-api-go-client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

const (
	SCHEDULES_ENDPOINT = "schedules/"
)

// getOnCallURLFromSettings retrieves the OnCall API URL from the Grafana settings endpoint.
// It makes a GET request to <grafana-url>/api/plugins/grafana-irm-app/settings and extracts
// the OnCall URL from the jsonData.onCallApiUrl field in the response.
// Returns the OnCall URL if found, or an error if the URL cannot be retrieved.
func getOnCallURLFromSettings(ctx context.Context, grafanaURL, grafanaAPIKey string) (string, error) {
	settingsURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/settings", strings.TrimRight(grafanaURL, "/"))

	req, err := http.NewRequestWithContext(ctx, "GET", settingsURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating settings request: %w", err)
	}

	if grafanaAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+grafanaAPIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code from settings API: %d", resp.StatusCode)
	}

	var settings struct {
		JsonData struct {
			OnCallApiUrl string `json:"onCallApiUrl"`
		} `json:"jsonData"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return "", fmt.Errorf("decoding settings response: %w", err)
	}

	if settings.JsonData.OnCallApiUrl == "" {
		return "", fmt.Errorf("OnCall API URL is not set in settings")
	}

	return settings.JsonData.OnCallApiUrl, nil
}

func oncallClientFromContext(ctx context.Context) (*aapi.Client, error) {
	// Get the standard Grafana URL and API key
	grafanaURL, grafanaAPIKey := mcpgrafana.GrafanaURLFromContext(ctx), mcpgrafana.GrafanaAPIKeyFromContext(ctx)

	// Try to get OnCall URL from settings endpoint
	grafanaOnCallURL, err := getOnCallURLFromSettings(ctx, grafanaURL, grafanaAPIKey)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall URL from settings: %w", err)
	}

	grafanaOnCallURL = strings.TrimRight(grafanaOnCallURL, "/")

	client, err := aapi.NewWithGrafanaURL(grafanaOnCallURL, grafanaAPIKey, grafanaURL)
	if err != nil {
		return nil, fmt.Errorf("creating OnCall client: %w", err)
	}

	return client, nil
}

type ListOnCallSchedulesParams struct {
	TeamID string `json:"teamId,omitempty" jsonschema:"description=The ID of the team to list schedules for"`
}

func listOnCallSchedules(ctx context.Context, args ListOnCallSchedulesParams) ([]*aapi.Schedule, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	listOptions := &aapi.ListScheduleOptions{}

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

type GetOnCallShiftParams struct {
	ShiftID string `json:"shiftId" jsonschema:"required,description=The ID of the shift to get details for"`
}

func getOnCallShift(ctx context.Context, args GetOnCallShiftParams) (*aapi.OnCallShift, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	shiftService := aapi.NewOnCallShiftService(client)
	shift, _, err := shiftService.GetOnCallShift(args.ShiftID, &aapi.GetOnCallShiftOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting OnCall shift %s: %w", args.ShiftID, err)
	}

	return shift, nil
}

var GetOnCallShift = mcpgrafana.MustTool(
	"get_oncall_shift",
	"Get details for a specific OnCall shift",
	getOnCallShift,
)

type GetCurrentOnCallUsersParams struct {
	ScheduleID string `json:"scheduleId" jsonschema:"required,description=The ID of the schedule to get current on-call users for"`
}

func getCurrentOnCallUsers(ctx context.Context, args GetCurrentOnCallUsersParams) (*aapi.Schedule, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	scheduleService := aapi.NewScheduleService(client)
	schedule, _, err := scheduleService.GetSchedule(args.ScheduleID, &aapi.GetScheduleOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting schedule %s: %w", args.ScheduleID, err)
	}

	return schedule, nil
}

var GetCurrentOnCallUsers = mcpgrafana.MustTool(
	"get_current_oncall_users",
	"Get users currently on-call for a specific schedule",
	getCurrentOnCallUsers,
)

type ListOnCallTeamsParams struct {
}

func listOnCallTeams(ctx context.Context, args ListOnCallTeamsParams) ([]*aapi.Team, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	teamService := aapi.NewTeamService(client)
	response, _, err := teamService.ListTeams(&aapi.ListTeamOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing OnCall teams: %w", err)
	}

	return response.Teams, nil
}

var ListOnCallTeams = mcpgrafana.MustTool(
	"list_oncall_teams",
	"List teams from Grafana OnCall",
	listOnCallTeams,
)

type ListOnCallUsersParams struct {
	UserID string `json:"userId,omitempty" jsonschema:"description=The ID of the user to get details for. If provided, returns only that user's details"`
}

func listOnCallUsers(ctx context.Context, args ListOnCallUsersParams) ([]*aapi.User, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	userService := aapi.NewUserService(client)

	if args.UserID != "" {
		user, _, err := userService.GetUser(args.UserID, &aapi.GetUserOptions{})
		if err != nil {
			return nil, fmt.Errorf("getting OnCall user %s: %w", args.UserID, err)
		}
		return []*aapi.User{user}, nil
	}

	// Otherwise, list all users
	response, _, err := userService.ListUsers(&aapi.ListUserOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing OnCall users: %w", err)
	}

	return response.Users, nil
}

var ListOnCallUsers = mcpgrafana.MustTool(
	"list_oncall_users",
	"List users from Grafana OnCall, optionally filtered by user ID",
	listOnCallUsers,
)

func AddOnCallTools(mcp *server.MCPServer) {
	ListOnCallSchedules.Register(mcp)
	GetOnCallShift.Register(mcp)
	GetCurrentOnCallUsers.Register(mcp)
	ListOnCallTeams.Register(mcp)
	ListOnCallUsers.Register(mcp)
}
