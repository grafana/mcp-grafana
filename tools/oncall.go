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
		JSONData struct {
			OnCallAPIURL string `json:"onCallApiUrl"`
		} `json:"jsonData"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return "", fmt.Errorf("decoding settings response: %w", err)
	}

	if settings.JSONData.OnCallAPIURL == "" {
		return "", fmt.Errorf("OnCall API URL is not set in settings")
	}

	return settings.JSONData.OnCallAPIURL, nil
}

func oncallClientFromContext(ctx context.Context) (*aapi.Client, error) {
	// Get the standard Grafana URL and API key
	var (
		grafanaURL    = mcpgrafana.GrafanaURLFromContext(ctx)
		grafanaAPIKey = mcpgrafana.GrafanaAPIKeyFromContext(ctx)
	)

	// Try to get OnCall URL from settings endpoint
	grafanaOnCallURL, err := getOnCallURLFromSettings(ctx, grafanaURL, grafanaAPIKey)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall URL from settings: %w", err)
	}

	grafanaOnCallURL = strings.TrimRight(grafanaOnCallURL, "/")

	// TODO: Allow access to OnCall using an access token instead of an API key.
	client, err := aapi.NewWithGrafanaURL(grafanaOnCallURL, grafanaAPIKey, grafanaURL)
	if err != nil {
		return nil, fmt.Errorf("creating OnCall client: %w", err)
	}

	return client, nil
}

// getUserServiceFromContext creates a new UserService using the OnCall client from the context
func getUserServiceFromContext(ctx context.Context) (*aapi.UserService, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	return aapi.NewUserService(client), nil
}

// getScheduleServiceFromContext creates a new ScheduleService using the OnCall client from the context
func getScheduleServiceFromContext(ctx context.Context) (*aapi.ScheduleService, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	return aapi.NewScheduleService(client), nil
}

// getTeamServiceFromContext creates a new TeamService using the OnCall client from the context
func getTeamServiceFromContext(ctx context.Context) (*aapi.TeamService, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	return aapi.NewTeamService(client), nil
}

// getOnCallShiftServiceFromContext creates a new OnCallShiftService using the OnCall client from the context
func getOnCallShiftServiceFromContext(ctx context.Context) (*aapi.OnCallShiftService, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	return aapi.NewOnCallShiftService(client), nil
}

type ListOnCallSchedulesParams struct {
	TeamID     string `json:"teamId,omitempty" jsonschema:"description=The ID of the team to list schedules for"`
	ScheduleID string `json:"scheduleId,omitempty" jsonschema:"description=The ID of the schedule to get details for. If provided\\, returns only that schedule's details"`
	Page       int    `json:"page,omitempty" jsonschema:"description=The page number to return (1-based)"`
}

// ScheduleSummary represents a simplified view of an OnCall schedule
type ScheduleSummary struct {
	ID       string   `json:"id" jsonschema:"description=The unique identifier of the schedule"`
	Name     string   `json:"name" jsonschema:"description=The name of the schedule"`
	TeamID   string   `json:"teamId" jsonschema:"description=The ID of the team this schedule belongs to"`
	Timezone string   `json:"timezone" jsonschema:"description=The timezone for this schedule"`
	Shifts   []string `json:"shifts" jsonschema:"description=List of shift IDs in this schedule"`
}

func listOnCallSchedules(ctx context.Context, args ListOnCallSchedulesParams) ([]*ScheduleSummary, error) {
	scheduleService, err := getScheduleServiceFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall schedule service: %w", err)
	}

	if args.ScheduleID != "" {
		schedule, _, err := scheduleService.GetSchedule(args.ScheduleID, &aapi.GetScheduleOptions{})
		if err != nil {
			return nil, fmt.Errorf("getting OnCall schedule %s: %w", args.ScheduleID, err)
		}
		summary := &ScheduleSummary{
			ID:       schedule.ID,
			Name:     schedule.Name,
			TeamID:   schedule.TeamId,
			Timezone: schedule.TimeZone,
		}
		if schedule.Shifts != nil {
			summary.Shifts = *schedule.Shifts
		}
		return []*ScheduleSummary{summary}, nil
	}

	listOptions := &aapi.ListScheduleOptions{}
	if args.Page > 0 {
		listOptions.Page = args.Page
	}
	if args.TeamID != "" {
		listOptions.TeamID = args.TeamID
	}

	response, _, err := scheduleService.ListSchedules(listOptions)
	if err != nil {
		return nil, fmt.Errorf("listing OnCall schedules: %w", err)
	}

	// Convert schedules to summaries
	summaries := make([]*ScheduleSummary, 0, len(response.Schedules))
	for _, schedule := range response.Schedules {
		summary := &ScheduleSummary{
			ID:       schedule.ID,
			Name:     schedule.Name,
			TeamID:   schedule.TeamId,
			Timezone: schedule.TimeZone,
		}
		if schedule.Shifts != nil {
			summary.Shifts = *schedule.Shifts
		}
		summaries = append(summaries, summary)
	}

	return summaries, nil
}

var ListOnCallSchedules = mcpgrafana.MustTool(
	"list_oncall_schedules",
	"List Grafana OnCall schedules, optionally filtering by team ID. If a specific schedule ID is provided, retrieves details for only that schedule. Returns a list of schedule summaries including ID, name, team ID, timezone, and shift IDs. Supports pagination.",
	listOnCallSchedules,
)

type GetOnCallShiftParams struct {
	ShiftID string `json:"shiftId" jsonschema:"required,description=The ID of the shift to get details for"`
}

func getOnCallShift(ctx context.Context, args GetOnCallShiftParams) (*aapi.OnCallShift, error) {
	shiftService, err := getOnCallShiftServiceFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall shift service: %w", err)
	}

	shift, _, err := shiftService.GetOnCallShift(args.ShiftID, &aapi.GetOnCallShiftOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting OnCall shift %s: %w", args.ShiftID, err)
	}

	return shift, nil
}

var GetOnCallShift = mcpgrafana.MustTool(
	"get_oncall_shift",
	"Get detailed information for a specific Grafana OnCall shift using its ID. A shift represents a designated time period within a schedule when users are actively on-call. Returns the full shift details.",
	getOnCallShift,
)

// CurrentOnCallUsers represents the currently on-call users for a schedule
type CurrentOnCallUsers struct {
	ScheduleID   string       `json:"scheduleId" jsonschema:"description=The ID of the schedule"`
	ScheduleName string       `json:"scheduleName" jsonschema:"description=The name of the schedule"`
	Users        []*aapi.User `json:"users" jsonschema:"description=List of users currently on call"`
}

type GetCurrentOnCallUsersParams struct {
	ScheduleID string `json:"scheduleId" jsonschema:"required,description=The ID of the schedule to get current on-call users for"`
}

func getCurrentOnCallUsers(ctx context.Context, args GetCurrentOnCallUsersParams) (*CurrentOnCallUsers, error) {
	scheduleService, err := getScheduleServiceFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall schedule service: %w", err)
	}

	schedule, _, err := scheduleService.GetSchedule(args.ScheduleID, &aapi.GetScheduleOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting schedule %s: %w", args.ScheduleID, err)
	}

	// Create the result with the schedule info
	result := &CurrentOnCallUsers{
		ScheduleID:   schedule.ID,
		ScheduleName: schedule.Name,
		Users:        make([]*aapi.User, 0, len(schedule.OnCallNow)),
	}

	// If there are no users on call, return early
	if len(schedule.OnCallNow) == 0 {
		return result, nil
	}

	// Get the user service to fetch user details
	userService, err := getUserServiceFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall user service: %w", err)
	}

	// Fetch details for each user currently on call
	for _, userID := range schedule.OnCallNow {
		user, _, err := userService.GetUser(userID, &aapi.GetUserOptions{})
		if err != nil {
			// Log the error but continue with other users
			fmt.Printf("Error fetching user %s: %v\n", userID, err)
			continue
		}
		result.Users = append(result.Users, user)
	}

	return result, nil
}

var GetCurrentOnCallUsers = mcpgrafana.MustTool(
	"get_current_oncall_users",
	"Get the list of users currently on-call for a specific Grafana OnCall schedule ID. Returns the schedule ID, name, and a list of detailed user objects for those currently on call.",
	getCurrentOnCallUsers,
)

type ListOnCallTeamsParams struct {
	Page int `json:"page,omitempty" jsonschema:"description=The page number to return"`
}

func listOnCallTeams(ctx context.Context, args ListOnCallTeamsParams) ([]*aapi.Team, error) {
	teamService, err := getTeamServiceFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall team service: %w", err)
	}

	listOptions := &aapi.ListTeamOptions{}
	if args.Page > 0 {
		listOptions.Page = args.Page
	}

	response, _, err := teamService.ListTeams(listOptions)
	if err != nil {
		return nil, fmt.Errorf("listing OnCall teams: %w", err)
	}

	return response.Teams, nil
}

var ListOnCallTeams = mcpgrafana.MustTool(
	"list_oncall_teams",
	"List teams configured in Grafana OnCall. Returns a list of team objects with their details. Supports pagination.",
	listOnCallTeams,
)

type ListOnCallUsersParams struct {
	UserID   string `json:"userId,omitempty" jsonschema:"description=The ID of the user to get details for. If provided\\, returns only that user's details"`
	Username string `json:"username,omitempty" jsonschema:"description=The username to filter users by. If provided\\, returns only the user matching this username"`
	Page     int    `json:"page,omitempty" jsonschema:"description=The page number to return"`
}

func listOnCallUsers(ctx context.Context, args ListOnCallUsersParams) ([]*aapi.User, error) {
	userService, err := getUserServiceFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall user service: %w", err)
	}

	if args.UserID != "" {
		user, _, err := userService.GetUser(args.UserID, &aapi.GetUserOptions{})
		if err != nil {
			return nil, fmt.Errorf("getting OnCall user %s: %w", args.UserID, err)
		}
		return []*aapi.User{user}, nil
	}

	// Otherwise, list all users
	listOptions := &aapi.ListUserOptions{}
	if args.Page > 0 {
		listOptions.Page = args.Page
	}
	if args.Username != "" {
		listOptions.Username = args.Username
	}

	response, _, err := userService.ListUsers(listOptions)
	if err != nil {
		return nil, fmt.Errorf("listing OnCall users: %w", err)
	}

	return response.Users, nil
}

var ListOnCallUsers = mcpgrafana.MustTool(
	"list_oncall_users",
	"List users from Grafana OnCall. Can retrieve all users, a specific user by ID, or filter by username. Returns a list of user objects with their details. Supports pagination.",
	listOnCallUsers,
)

func AddOnCallTools(mcp *server.MCPServer) {
	ListOnCallSchedules.Register(mcp)
	GetOnCallShift.Register(mcp)
	GetCurrentOnCallUsers.Register(mcp)
	ListOnCallTeams.Register(mcp)
	ListOnCallUsers.Register(mcp)
}
