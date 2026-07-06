package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	aapi "github.com/grafana/amixr-api-go-client"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// getOnCallURLFromSettings retrieves the OnCall API URL from the Grafana settings endpoint.
// Only used for the public API fallback path (when no OBO tokens are available).
func getOnCallURLFromSettings(ctx context.Context, cfg mcpgrafana.GrafanaConfig) (string, error) {
	settingsURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/settings", cfg.URL)

	req, err := http.NewRequestWithContext(ctx, "GET", settingsURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating settings request: %w", err)
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return "", fmt.Errorf("building transport: %w", err)
	}

	httpClient := &http.Client{Transport: transport}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching settings: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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

// oncallClientFromContext creates an amixr client for the public OnCall API.
// Used as fallback when OBO auth is not available (OSS / self-hosted with API key).
func oncallClientFromContext(ctx context.Context) (*aapi.Client, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	grafanaOnCallURL, err := getOnCallURLFromSettings(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall URL from settings: %w", err)
	}

	grafanaOnCallURL = strings.TrimRight(grafanaOnCallURL, "/")

	client, err := aapi.NewWithGrafanaURL(grafanaOnCallURL, cfg.APIKey, cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("creating OnCall client: %w", err)
	}

	// Customize the HTTP client's transport using reflection since the
	// OnCall client doesn't expose its HTTP client directly. Auth is
	// handled by the OnCall library (API key passed above), so we skip it.
	clientValue := reflect.ValueOf(client)
	if clientValue.Kind() == reflect.Ptr && !clientValue.IsNil() {
		clientValue = clientValue.Elem()
		if clientValue.Kind() == reflect.Struct {
			httpClientField := clientValue.FieldByName("HTTPClient")
			if !httpClientField.IsValid() {
				httpClientField = clientValue.FieldByName("HttpClient")
			}
			if !httpClientField.IsValid() {
				httpClientField = clientValue.FieldByName("Client")
			}
			if httpClientField.IsValid() && httpClientField.CanSet() {
				if httpClient, ok := httpClientField.Interface().(*http.Client); ok {
					transport, err := mcpgrafana.BuildTransport(&cfg, nil, mcpgrafana.WithoutAuth())
					if err != nil {
						mcpgrafana.LoggerFromContext(ctx).Error("Failed to build transport for OnCall client", "error", err)
					} else {
						httpClient.Transport = transport
					}
				}
			}
		}
	}

	return client, nil
}

// --- Schedules ---

type ListOnCallSchedulesParams struct {
	TeamID     string `json:"teamId,omitempty" jsonschema:"description=The ID of the team to list schedules for"`
	ScheduleID string `json:"scheduleId,omitempty" jsonschema:"description=The ID of the schedule to get details for. If provided\\, returns only that schedule's details"`
	Page       int    `json:"page,omitempty" jsonschema:"description=The page number to return (1-based)"`
}

// ScheduleSummary represents a simplified view of an OnCall schedule.
type ScheduleSummary struct {
	ID       string   `json:"id" jsonschema:"description=The unique identifier of the schedule"`
	Name     string   `json:"name" jsonschema:"description=The name of the schedule"`
	TeamID   string   `json:"teamId" jsonschema:"description=The ID of the team this schedule belongs to"`
	Timezone string   `json:"timezone" jsonschema:"description=The timezone for this schedule"`
	Shifts   []string `json:"shifts" jsonschema:"description=List of shift IDs in this schedule"`
}

func listOnCallSchedules(ctx context.Context, args ListOnCallSchedulesParams) ([]*ScheduleSummary, error) {
	if useOncallProxy(ctx) {
		return proxyListSchedules(ctx, args)
	}
	return amixrListSchedules(ctx, args)
}

func amixrListSchedules(ctx context.Context, args ListOnCallSchedulesParams) ([]*ScheduleSummary, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	scheduleService := aapi.NewScheduleService(client)

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
	mcp.WithTitleAnnotation("List OnCall schedules"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- Shifts ---

type GetOnCallShiftParams struct {
	ShiftID string `json:"shiftId" jsonschema:"required,description=The ID of the shift to get details for"`
}

func getOnCallShift(ctx context.Context, args GetOnCallShiftParams) (*OnCallShift, error) {
	if useOncallProxy(ctx) {
		return proxyGetShift(ctx, args.ShiftID)
	}
	return amixrGetShift(ctx, args.ShiftID)
}

func amixrGetShift(ctx context.Context, shiftID string) (*OnCallShift, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	shiftService := aapi.NewOnCallShiftService(client)
	shift, _, err := shiftService.GetOnCallShift(shiftID, &aapi.GetOnCallShiftOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting OnCall shift %s: %w", shiftID, err)
	}

	return &OnCallShift{
		ID:            shift.ID,
		Name:          shift.Name,
		Type:          shift.Type,
		PriorityLevel: shift.Level,
		ShiftStart:    shift.Start,
		RotationStart: shift.Start, // amixr only has one "start" field
		Frequency:     shift.Frequency,
		Interval:      derefIntOr(shift.Interval, 0),
		ByDay:         derefStrSlice(shift.ByDay),
		WeekStart:     derefStrOr(shift.WeekStart, ""),
		RollingUsers:  shift.RollingUsers,
		Until:         derefStrOr(shift.Until, ""),
	}, nil
}

var GetOnCallShift = mcpgrafana.MustTool(
	"get_oncall_shift",
	"Get detailed information for a specific Grafana OnCall shift using its ID. A shift represents a designated time period within a schedule when users are actively on-call. Returns the full shift details.",
	getOnCallShift,
	mcp.WithTitleAnnotation("Get OnCall shift"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- Current On-Call Users ---

// CurrentOnCallUsers represents the currently on-call users for a schedule.
type CurrentOnCallUsers struct {
	ScheduleID   string        `json:"scheduleId" jsonschema:"description=The ID of the schedule"`
	ScheduleName string        `json:"scheduleName" jsonschema:"description=The name of the schedule"`
	Users        []*OnCallUser `json:"users" jsonschema:"description=List of users currently on call"`
}

type GetCurrentOnCallUsersParams struct {
	ScheduleID string `json:"scheduleId" jsonschema:"required,description=The ID of the schedule to get current on-call users for"`
}

func getCurrentOnCallUsers(ctx context.Context, args GetCurrentOnCallUsersParams) (*CurrentOnCallUsers, error) {
	if useOncallProxy(ctx) {
		return proxyGetCurrentOnCallUsers(ctx, args.ScheduleID)
	}
	return amixrGetCurrentOnCallUsers(ctx, args.ScheduleID)
}

func amixrGetCurrentOnCallUsers(ctx context.Context, scheduleID string) (*CurrentOnCallUsers, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	scheduleService := aapi.NewScheduleService(client)
	schedule, _, err := scheduleService.GetSchedule(scheduleID, &aapi.GetScheduleOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting schedule %s: %w", scheduleID, err)
	}

	result := &CurrentOnCallUsers{
		ScheduleID:   schedule.ID,
		ScheduleName: schedule.Name,
		Users:        make([]*OnCallUser, 0, len(schedule.OnCallNow)),
	}

	if len(schedule.OnCallNow) == 0 {
		return result, nil
	}

	userService := aapi.NewUserService(client)
	for _, userID := range schedule.OnCallNow {
		user, _, err := userService.GetUser(userID, &aapi.GetUserOptions{})
		if err != nil {
			continue
		}
		result.Users = append(result.Users, &OnCallUser{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
		})
	}

	return result, nil
}

var GetCurrentOnCallUsers = mcpgrafana.MustTool(
	"get_current_oncall_users",
	"Get the list of users currently on-call for a specific Grafana OnCall schedule ID. Returns the schedule ID, name, and a list of detailed user objects for those currently on call.",
	getCurrentOnCallUsers,
	mcp.WithTitleAnnotation("Get current on-call users"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- Teams ---

type ListOnCallTeamsParams struct {
	Page int `json:"page,omitempty" jsonschema:"description=The page number to return"`
}

func listOnCallTeams(ctx context.Context, args ListOnCallTeamsParams) ([]*OnCallTeam, error) {
	if useOncallProxy(ctx) {
		return proxyListTeams(ctx, args)
	}
	return amixrListTeams(ctx, args)
}

func amixrListTeams(ctx context.Context, args ListOnCallTeamsParams) ([]*OnCallTeam, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	teamService := aapi.NewTeamService(client)
	listOptions := &aapi.ListTeamOptions{}
	if args.Page > 0 {
		listOptions.Page = args.Page
	}

	response, _, err := teamService.ListTeams(listOptions)
	if err != nil {
		return nil, fmt.Errorf("listing OnCall teams: %w", err)
	}

	result := make([]*OnCallTeam, 0, len(response.Teams))
	for _, team := range response.Teams {
		result = append(result, &OnCallTeam{
			ID:        team.ID,
			Name:      team.Name,
			Email:     team.Email,
			AvatarURL: team.AvatarUrl,
		})
	}
	return result, nil
}

var ListOnCallTeams = mcpgrafana.MustTool(
	"list_oncall_teams",
	"List teams configured in Grafana OnCall. Returns a list of team objects with their details. Supports pagination.",
	listOnCallTeams,
	mcp.WithTitleAnnotation("List OnCall teams"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- Users ---

type ListOnCallUsersParams struct {
	UserID   string `json:"userId,omitempty" jsonschema:"description=The ID of the user to get details for. If provided\\, returns only that user's details"`
	Username string `json:"username,omitempty" jsonschema:"description=The username to filter users by. If provided\\, returns only the user matching this username"`
	Page     int    `json:"page,omitempty" jsonschema:"description=The page number to return"`
}

func listOnCallUsers(ctx context.Context, args ListOnCallUsersParams) ([]*OnCallUser, error) {
	if useOncallProxy(ctx) {
		return proxyListUsers(ctx, args)
	}
	return amixrListUsers(ctx, args)
}

func amixrListUsers(ctx context.Context, args ListOnCallUsersParams) ([]*OnCallUser, error) {
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
		return []*OnCallUser{{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
		}}, nil
	}

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

	result := make([]*OnCallUser, 0, len(response.Users))
	for _, user := range response.Users {
		result = append(result, &OnCallUser{
			ID:       user.ID,
			Username: user.Username,
			Email:    user.Email,
			Role:     user.Role,
		})
	}
	return result, nil
}

var ListOnCallUsers = mcpgrafana.MustTool(
	"list_oncall_users",
	"List users from Grafana OnCall. These are OnCall users (separate from Grafana users). Can retrieve all users in the OnCall directory, a specific user by ID, or filter by username. Returns a list of user objects with their details. Supports pagination.",
	listOnCallUsers,
	mcp.WithTitleAnnotation("List OnCall users"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// --- Alert Groups ---

type ListAlertGroupsParams struct {
	Page          int      `json:"page,omitempty" jsonschema:"description=The page number to return"`
	AlertGroupID  string   `json:"id,omitempty" jsonschema:"description=Filter by specific alert group ID"`
	RouteID       string   `json:"routeId,omitempty" jsonschema:"description=Filter by route ID"`
	IntegrationID string   `json:"integrationId,omitempty" jsonschema:"description=Filter by integration ID"`
	State         string   `json:"state,omitempty" jsonschema:"description=Filter by alert group state (one of: new\\, acknowledged\\, resolved\\, silenced)"`
	TeamID        string   `json:"teamId,omitempty" jsonschema:"description=Filter by team ID"`
	StartedAt     string   `json:"startedAt,omitempty" jsonschema:"description=Filter by time range in format '{start}_{end}' ISO 8601 timestamp range (UTC assumed\\, no timezone indicator needed) (e.g.\\, '2025-01-19T00:00:00_2025-01-19T23:59:59')"`
	Labels        []string `json:"labels,omitempty" jsonschema:"description=Filter by labels in format key:value (e.g.\\, ['env:prod'\\, 'severity:high'])"`
	Name          string   `json:"name,omitempty" jsonschema:"description=Filter by alert group name"`
}

func listAlertGroups(ctx context.Context, args ListAlertGroupsParams) ([]*OnCallAlertGroup, error) {
	if useOncallProxy(ctx) {
		return proxyListAlertGroups(ctx, args)
	}
	return amixrListAlertGroups(ctx, args)
}

func amixrListAlertGroups(ctx context.Context, args ListAlertGroupsParams) ([]*OnCallAlertGroup, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	alertGroupService := aapi.NewAlertGroupService(client)

	listOptions := &aapi.ListAlertGroupOptions{}
	if args.Page > 0 {
		listOptions.Page = args.Page
	}
	if args.AlertGroupID != "" {
		listOptions.AlertGroupID = args.AlertGroupID
	}
	if args.RouteID != "" {
		listOptions.RouteID = args.RouteID
	}
	if args.IntegrationID != "" {
		listOptions.IntegrationID = args.IntegrationID
	}
	if args.State != "" {
		listOptions.State = args.State
	}
	if args.TeamID != "" {
		listOptions.TeamID = args.TeamID
	}
	if args.StartedAt != "" {
		listOptions.StartedAt = args.StartedAt
	}
	if len(args.Labels) > 0 {
		listOptions.Labels = args.Labels
	}
	if args.Name != "" {
		listOptions.Name = args.Name
	}

	response, _, err := alertGroupService.ListAlertGroups(listOptions)
	if err != nil {
		return nil, fmt.Errorf("listing OnCall alert groups: %w", err)
	}

	result := make([]*OnCallAlertGroup, 0, len(response.AlertGroups))
	for _, ag := range response.AlertGroups {
		result = append(result, &OnCallAlertGroup{
			ID:             ag.ID,
			IntegrationID:  ag.IntegrationID,
			AlertsCount:    ag.AlertsCount,
			State:          ag.State,
			CreatedAt:      ag.CreatedAt,
			ResolvedAt:     ag.ResolvedAt,
			AcknowledgedAt: ag.AcknowledgedAt,
			Title:          ag.Title,
			Permalinks:     ag.Permalinks,
		})
	}
	return result, nil
}

var ListAlertGroups = mcpgrafana.MustTool(
	"list_alert_groups",
	"List alert groups from Grafana OnCall with filtering options. Supports filtering by alert group ID, route ID, integration ID, state (new, acknowledged, resolved, silenced), team ID, time range, labels, and name. For time ranges, use format '{start}_{end}' ISO 8601 timestamp range (e.g., '2025-01-19T00:00:00_2025-01-19T23:59:59' for a specific day). For labels, use format 'key:value' (e.g., ['env:prod', 'severity:high']). Returns a list of alert group objects with their details. Supports pagination.",
	listAlertGroups,
	mcp.WithTitleAnnotation("List IRM alert groups"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type GetAlertGroupParams struct {
	AlertGroupID string `json:"alertGroupId" jsonschema:"required,description=The ID of the alert group to retrieve"`
}

func getAlertGroup(ctx context.Context, args GetAlertGroupParams) (*OnCallAlertGroup, error) {
	if useOncallProxy(ctx) {
		return proxyGetAlertGroup(ctx, args.AlertGroupID)
	}
	return amixrGetAlertGroup(ctx, args.AlertGroupID)
}

func amixrGetAlertGroup(ctx context.Context, alertGroupID string) (*OnCallAlertGroup, error) {
	client, err := oncallClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall client: %w", err)
	}

	alertGroupService := aapi.NewAlertGroupService(client)
	ag, _, err := alertGroupService.GetAlertGroup(alertGroupID)
	if err != nil {
		return nil, fmt.Errorf("getting OnCall alert group %s: %w", alertGroupID, err)
	}

	return &OnCallAlertGroup{
		ID:             ag.ID,
		IntegrationID:  ag.IntegrationID,
		AlertsCount:    ag.AlertsCount,
		State:          ag.State,
		CreatedAt:      ag.CreatedAt,
		ResolvedAt:     ag.ResolvedAt,
		AcknowledgedAt: ag.AcknowledgedAt,
		Title:          ag.Title,
		Permalinks:     ag.Permalinks,
	}, nil
}

var GetAlertGroup = mcpgrafana.MustTool(
	"get_alert_group",
	"Get a specific alert group from Grafana OnCall by its ID. Returns the full alert group details.",
	getAlertGroup,
	mcp.WithTitleAnnotation("Get IRM alert group"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddOnCallTools(mcp *server.MCPServer) {
	ListOnCallSchedules.Register(mcp)
	GetOnCallShift.Register(mcp)
	GetCurrentOnCallUsers.Register(mcp)
	ListOnCallTeams.Register(mcp)
	ListOnCallUsers.Register(mcp)
	ListAlertGroups.Register(mcp)
	GetAlertGroup.Register(mcp)
}

// helpers for converting amixr pointer types

func derefStrOr(p *string, fallback string) string {
	if p != nil {
		return *p
	}
	return fallback
}

func derefIntOr(p *int, fallback int) int {
	if p != nil {
		return *p
	}
	return fallback
}

func derefStrSlice(p *[]string) []string {
	if p != nil {
		return *p
	}
	return nil
}
