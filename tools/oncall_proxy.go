package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// IRM plugin proxy paths. The proxy prepends "api/internal/v1/" before forwarding.
const (
	irmProxyBasePath = "/api/plugins/grafana-irm-app/resources"

	proxyAlertGroupsPath = "alertgroups/"
	proxySchedulesPath   = "schedules/"
	proxyShiftsPath      = "oncall_shifts/"
	proxyUsersPath       = "users/"
	proxyTeamsPath       = "teams/"
)

// stateToStatus maps public API state strings to internal API numeric status values.
// The internal API's status filter expects integers: 0=new, 1=acknowledged, 2=resolved, 3=silenced.
var stateToStatus = map[string]string{
	"new":          "0",
	"acknowledged": "1",
	"resolved":     "2",
	"silenced":     "3",
}

// oncallProxyClient makes requests to the OnCall internal API via the IRM plugin proxy.
type oncallProxyClient struct {
	httpClient *http.Client
	baseURL    string
}

func newOncallProxyClient(ctx context.Context) (*oncallProxyClient, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("building transport: %w", err)
	}

	return &oncallProxyClient{
		httpClient: &http.Client{Transport: transport},
		baseURL:    cfg.URL + irmProxyBasePath,
	}, nil
}

func (c *oncallProxyClient) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	reqURL := c.baseURL + "/" + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

// paginatedResult is the standard paginated response from the internal OnCall API.
type paginatedResult[T any] struct {
	Results []T     `json:"results"`
	Next    *string `json:"next"`
}

// extractNextPath extracts the relative path from pagination URLs.
// The backend returns absolute URLs pointing to the real OnCall host;
// we strip the prefix and re-request through the proxy.
func extractNextPath(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid pagination URL %q: %w", rawURL, err)
	}
	const marker = "/api/internal/v1/"
	if idx := strings.Index(parsed.Path, marker); idx >= 0 {
		path := parsed.Path[idx+len(marker):]
		if parsed.RawQuery != "" {
			path += "?" + parsed.RawQuery
		}
		return path, nil
	}
	path := strings.TrimPrefix(parsed.Path, "/")
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	return path, nil
}

func handleProxyErrorResponse(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return fmt.Errorf("request failed with status %d (could not read body: %w)", resp.StatusCode, err)
	}
	if len(body) > 0 {
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}
	return fmt.Errorf("request failed with status %d", resp.StatusCode)
}

// fetchSinglePage fetches a single page from a paginated endpoint (no auto-follow).
func fetchSinglePage[T any](ctx context.Context, c *oncallProxyClient, path string) ([]T, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	body, readErr := readResponseBody(resp.Body, defaultResponseLimitBytes)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if len(body) > 0 {
			return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
		}
		if readErr != nil {
			return nil, fmt.Errorf("request failed with status %d (%w)", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("request failed with status %d", resp.StatusCode)
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading response: %w", readErr)
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var items []T
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		return items, nil
	}

	var page paginatedResult[T]
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return page.Results, nil
}

// fetchPaginated fetches all pages from a paginated endpoint.
func fetchPaginated[T any](ctx context.Context, c *oncallProxyClient, path string) ([]T, error) {
	var all []T
	next := path
	for next != "" {
		resp, err := c.doRequest(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		body, readErr := readResponseBody(resp.Body, defaultResponseLimitBytes)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if len(body) > 0 {
				return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
			}
			if readErr != nil {
				return nil, fmt.Errorf("request failed with status %d (%w)", resp.StatusCode, readErr)
			}
			return nil, fmt.Errorf("request failed with status %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, fmt.Errorf("reading response: %w", readErr)
		}

		// The internal API returns either paginated {"results": [...], "next": "..."} or a raw array.
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var items []T
			if err := json.Unmarshal(body, &items); err != nil {
				return nil, fmt.Errorf("decoding response: %w", err)
			}
			all = append(all, items...)
			break
		}

		var page paginatedResult[T]
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		all = append(all, page.Results...)

		if page.Next == nil || *page.Next == "" {
			break
		}
		next, err = extractNextPath(*page.Next)
		if err != nil {
			return nil, fmt.Errorf("pagination: %w", err)
		}
	}
	return all, nil
}

// fetchOne fetches a single resource by ID.
func fetchOne[T any](ctx context.Context, c *oncallProxyClient, basePath, id string) (*T, error) {
	path := fmt.Sprintf("%s%s/", basePath, url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, handleProxyErrorResponse(resp)
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// --- Proxy implementations for each tool ---

func proxyListAlertGroups(ctx context.Context, args ListAlertGroupsParams) ([]*OnCallAlertGroup, error) {
	client, err := newOncallProxyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}

	params := url.Values{}
	if args.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", args.Page))
	}
	if args.AlertGroupID != "" {
		params.Set("id", args.AlertGroupID)
	}
	if args.RouteID != "" {
		params.Set("route_id", args.RouteID)
	}
	if args.IntegrationID != "" {
		params.Set("integration_id", args.IntegrationID)
	}
	if args.State != "" {
		v, ok := stateToStatus[args.State]
		if !ok {
			return nil, fmt.Errorf("invalid alert group state %q: must be one of new, acknowledged, resolved, silenced", args.State)
		}
		params.Set("status", v)
	}
	if args.TeamID != "" {
		params.Set("team", args.TeamID)
	}
	if args.StartedAt != "" {
		params.Set("started_at", args.StartedAt)
	}
	if args.Name != "" {
		params.Set("search", args.Name)
	}
	for _, label := range args.Labels {
		params.Add("label", label)
	}

	path := proxyAlertGroupsPath
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var internal []onCallAlertGroupInternal
	var err2 error
	if args.Page > 0 {
		internal, err2 = fetchSinglePage[onCallAlertGroupInternal](ctx, client, path)
	} else {
		internal, err2 = fetchPaginated[onCallAlertGroupInternal](ctx, client, path)
	}
	if err2 != nil {
		return nil, fmt.Errorf("listing alert groups: %w", err2)
	}

	result := make([]*OnCallAlertGroup, 0, len(internal))
	for i := range internal {
		result = append(result, internal[i].toOnCallAlertGroup())
	}
	return result, nil
}

func proxyGetAlertGroup(ctx context.Context, id string) (*OnCallAlertGroup, error) {
	client, err := newOncallProxyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}

	internal, err := fetchOne[onCallAlertGroupInternal](ctx, client, proxyAlertGroupsPath, id)
	if err != nil {
		return nil, fmt.Errorf("getting alert group %s: %w", id, err)
	}
	return internal.toOnCallAlertGroup(), nil
}

func proxyListSchedules(ctx context.Context, args ListOnCallSchedulesParams) ([]*ScheduleSummary, error) {
	client, err := newOncallProxyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}

	if args.ScheduleID != "" {
		schedule, err := fetchOne[onCallScheduleInternal](ctx, client, proxySchedulesPath, args.ScheduleID)
		if err != nil {
			return nil, fmt.Errorf("getting schedule %s: %w", args.ScheduleID, err)
		}
		return []*ScheduleSummary{internalScheduleToSummary(schedule)}, nil
	}

	params := url.Values{}
	if args.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", args.Page))
	}
	if args.TeamID != "" {
		params.Set("team", args.TeamID)
	}
	path := proxySchedulesPath
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var schedules []onCallScheduleInternal
	if args.Page > 0 {
		schedules, err = fetchSinglePage[onCallScheduleInternal](ctx, client, path)
	} else {
		schedules, err = fetchPaginated[onCallScheduleInternal](ctx, client, path)
	}
	if err != nil {
		return nil, fmt.Errorf("listing schedules: %w", err)
	}

	result := make([]*ScheduleSummary, 0, len(schedules))
	for i := range schedules {
		result = append(result, internalScheduleToSummary(&schedules[i]))
	}
	return result, nil
}

func internalScheduleToSummary(s *onCallScheduleInternal) *ScheduleSummary {
	summary := &ScheduleSummary{
		ID:       s.ID,
		Name:     s.Name,
		Timezone: s.TimeZone,
		Shifts:   s.Shifts,
	}
	if t, ok := s.Team.(string); ok {
		summary.TeamID = t
	}
	if summary.Shifts == nil {
		summary.Shifts = []string{}
	}
	return summary
}

func proxyGetShift(ctx context.Context, id string) (*OnCallShift, error) {
	client, err := newOncallProxyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}
	internal, err := fetchOne[onCallShiftInternal](ctx, client, proxyShiftsPath, id)
	if err != nil {
		return nil, fmt.Errorf("getting shift %s: %w", id, err)
	}
	return internal.toOnCallShift(), nil
}

func proxyListUsers(ctx context.Context, args ListOnCallUsersParams) ([]*OnCallUser, error) {
	client, err := newOncallProxyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}

	if args.UserID != "" {
		internal, err := fetchOne[onCallUserInternal](ctx, client, proxyUsersPath, args.UserID)
		if err != nil {
			return nil, fmt.Errorf("getting user %s: %w", args.UserID, err)
		}
		return []*OnCallUser{internal.toOnCallUser()}, nil
	}

	params := url.Values{}
	if args.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", args.Page))
	}
	if args.Username != "" {
		params.Set("search", args.Username)
	}
	path := proxyUsersPath
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var internal []onCallUserInternal
	if args.Page > 0 {
		internal, err = fetchSinglePage[onCallUserInternal](ctx, client, path)
	} else {
		internal, err = fetchPaginated[onCallUserInternal](ctx, client, path)
	}
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}

	result := make([]*OnCallUser, 0, len(internal))
	for i := range internal {
		u := internal[i].toOnCallUser()
		if args.Username != "" && u.Username != args.Username {
			continue
		}
		result = append(result, u)
	}
	return result, nil
}

func proxyListTeams(ctx context.Context, args ListOnCallTeamsParams) ([]*OnCallTeam, error) {
	client, err := newOncallProxyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}

	params := url.Values{}
	if args.Page > 0 {
		params.Set("page", fmt.Sprintf("%d", args.Page))
	}
	path := proxyTeamsPath
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	var internal []onCallTeamInternal
	if args.Page > 0 {
		internal, err = fetchSinglePage[onCallTeamInternal](ctx, client, path)
	} else {
		internal, err = fetchPaginated[onCallTeamInternal](ctx, client, path)
	}
	if err != nil {
		return nil, fmt.Errorf("listing teams: %w", err)
	}

	result := make([]*OnCallTeam, 0, len(internal))
	for i := range internal {
		result = append(result, internal[i].toOnCallTeam())
	}
	return result, nil
}

func proxyGetCurrentOnCallUsers(ctx context.Context, scheduleID string) (*CurrentOnCallUsers, error) {
	client, err := newOncallProxyClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}

	schedule, err := fetchOne[onCallScheduleInternal](ctx, client, proxySchedulesPath, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("getting schedule %s: %w", scheduleID, err)
	}

	result := &CurrentOnCallUsers{
		ScheduleID:   schedule.ID,
		ScheduleName: schedule.Name,
		Users:        make([]*OnCallUser, 0),
	}

	userIDs := extractUserIDs(schedule.OnCallNow)
	for _, userID := range userIDs {
		internal, err := fetchOne[onCallUserInternal](ctx, client, proxyUsersPath, userID)
		if err != nil {
			mcpgrafana.LoggerFromContext(ctx).Warn("Failed to fetch on-call user", "user_id", userID, "error", err)
			continue
		}
		result.Users = append(result.Users, internal.toOnCallUser())
	}

	return result, nil
}

// extractUserIDs extracts user ID strings from the on_call_now field,
// which can be []string or []any depending on the API.
func extractUserIDs(onCallNow any) []string {
	switch v := onCallNow.(type) {
	case []string:
		return v
	case []any:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				ids = append(ids, s)
			}
		}
		return ids
	}
	return nil
}

// useOncallProxy returns true when OBO auth tokens are available,
// meaning we should route through the IRM plugin proxy.
func useOncallProxy(ctx context.Context) bool {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	return cfg.AccessToken != "" && cfg.IDToken != ""
}
