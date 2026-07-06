package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractNextPath(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    string
		wantErr bool
	}{
		{
			name:   "standard pagination URL",
			rawURL: "https://oncall-prod-us-east-0.grafana.net/oncall/api/internal/v1/alertgroups/?cursor=abc123",
			want:   "alertgroups/?cursor=abc123",
		},
		{
			name:   "pagination with page number",
			rawURL: "https://oncall-prod.grafana.net/oncall/api/internal/v1/schedules/?page=2",
			want:   "schedules/?page=2",
		},
		{
			name:   "no query params",
			rawURL: "https://oncall.grafana.net/oncall/api/internal/v1/users/",
			want:   "users/",
		},
		{
			name:   "fallback for non-standard URL",
			rawURL: "https://example.com/some/other/path?page=2",
			want:   "some/other/path?page=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractNextPath(tt.rawURL)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAlertGroupStatusToState(t *testing.T) {
	tests := []struct {
		status any
		want   string
	}{
		{float64(0), "new"},
		{float64(1), "acknowledged"},
		{float64(2), "resolved"},
		{float64(3), "silenced"},
		{float64(99), "unknown"},
		{"resolved", "resolved"},
		{nil, "unknown"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%v", tt.status), func(t *testing.T) {
			got := alertGroupStatusToState(tt.status)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestInternalAlertGroupConversion(t *testing.T) {
	ag := &onCallAlertGroupInternal{
		PK:          "ABC123",
		AlertsCount: 5,
		Status:      float64(2),
		StartedAt:   "2025-01-15T10:00:00Z",
		ResolvedAt:  "2025-01-15T11:00:00Z",
		SilencedAt:  "",
		AlertReceiveChannel: map[string]any{
			"id":   "INT456",
			"name": "My Integration",
		},
		Team: "TEAM789",
		RenderForWeb: &renderForWeb{
			Title:   "High CPU Alert",
			Message: "CPU usage exceeded 90%",
		},
		Permalinks: map[string]string{
			"slack": "https://slack.com/archives/C123",
			"web":   "https://grafana.com/a/grafana-irm-app/alert-groups/ABC123",
		},
	}

	result := ag.toOnCallAlertGroup()

	assert.Equal(t, "ABC123", result.ID)
	assert.Equal(t, 5, result.AlertsCount)
	assert.Equal(t, "resolved", result.State)
	assert.Equal(t, "2025-01-15T10:00:00Z", result.CreatedAt)
	assert.Equal(t, "2025-01-15T11:00:00Z", result.ResolvedAt)
	assert.Equal(t, "INT456", result.IntegrationID)
	assert.Equal(t, "TEAM789", result.Team)
	assert.Equal(t, "High CPU Alert", result.Title)
	assert.Equal(t, "https://slack.com/archives/C123", result.Permalinks["slack"])
}

func TestInternalUserConversion(t *testing.T) {
	u := &onCallUserInternal{
		PK:       "USER123",
		Username: "testuser",
		Email:    "test@example.com",
		Name:     "Test User",
		Role:     "admin",
	}

	result := u.toOnCallUser()

	assert.Equal(t, "USER123", result.ID)
	assert.Equal(t, "testuser", result.Username)
	assert.Equal(t, "test@example.com", result.Email)
	assert.Equal(t, "Test User", result.Name)
	assert.Equal(t, "admin", result.Role)
}

func TestInternalShiftConversion(t *testing.T) {
	s := &onCallShiftInternal{
		ID:            "SHIFT1",
		Name:          "Morning Shift",
		Type:          float64(2),
		Schedule:      "SCHED1",
		PriorityLevel: 1,
		ShiftStart:    "2025-01-15T08:00:00Z",
		ShiftEnd:      "2025-01-15T16:00:00Z",
		RotationStart: "2025-01-01T08:00:00Z",
		Until:         "2025-06-01T00:00:00Z",
		Frequency:     "weekly",
		Interval:      1,
		ByDay:         []string{"MO", "TU", "WE", "TH", "FR"},
		WeekStart:     "MO",
		RollingUsers:  []any{[]any{"USER1", "USER2"}},
	}

	result := s.toOnCallShift()

	assert.Equal(t, "SHIFT1", result.ID)
	assert.Equal(t, "Morning Shift", result.Name)
	assert.Equal(t, float64(2), result.Type)
	assert.Equal(t, "SCHED1", result.Schedule)
	assert.Equal(t, 1, result.PriorityLevel)
	assert.Equal(t, "2025-01-15T08:00:00Z", result.ShiftStart)
	assert.Equal(t, "2025-01-15T16:00:00Z", result.ShiftEnd)
	assert.Equal(t, "2025-01-01T08:00:00Z", result.RotationStart)
	assert.Equal(t, "2025-06-01T00:00:00Z", result.Until)
	assert.Equal(t, "weekly", result.Frequency)
	assert.Equal(t, 1, result.Interval)
	assert.Equal(t, []string{"MO", "TU", "WE", "TH", "FR"}, result.ByDay)
	assert.Equal(t, "MO", result.WeekStart)
}

func TestProxyUsernameExactMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(paginatedResult[onCallUserInternal]{
			Results: []onCallUserInternal{
				{PK: "U1", Username: "ben"},
				{PK: "U2", Username: "benjamin"},
				{PK: "U3", Username: "benny"},
			},
		})
	}))
	defer server.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:         server.URL,
		AccessToken: "test-access",
		IDToken:     "test-id",
	})

	result, err := proxyListUsers(ctx, ListOnCallUsersParams{Username: "ben"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "U1", result[0].ID)
	assert.Equal(t, "ben", result[0].Username)
}

func TestExtractUserIDs(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
	}{
		{
			name:  "string slice",
			input: []string{"user1", "user2"},
			want:  []string{"user1", "user2"},
		},
		{
			name:  "any slice",
			input: []any{"user1", "user2"},
			want:  []string{"user1", "user2"},
		},
		{
			name:  "nil",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty any slice",
			input: []any{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUserIDs(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUseOncallProxy(t *testing.T) {
	t.Run("with OBO tokens", func(t *testing.T) {
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
			URL:         "https://example.grafana.net",
			AccessToken: "access-token",
			IDToken:     "id-token",
		})
		assert.True(t, useOncallProxy(ctx))
	})

	t.Run("with API key only", func(t *testing.T) {
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
			URL:    "https://example.grafana.net",
			APIKey: "my-api-key",
		})
		assert.False(t, useOncallProxy(ctx))
	})

	t.Run("with no auth", func(t *testing.T) {
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
			URL: "https://example.grafana.net",
		})
		assert.False(t, useOncallProxy(ctx))
	})
}

func TestProxyListAlertGroups(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/api/plugins/grafana-irm-app/resources/alertgroups/")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(paginatedResult[onCallAlertGroupInternal]{
			Results: []onCallAlertGroupInternal{
				{
					PK:          "AG1",
					AlertsCount: 3,
					Status:      float64(0),
					StartedAt:   "2025-01-15T10:00:00Z",
					RenderForWeb: &renderForWeb{
						Title: "Test Alert",
					},
					Permalinks: map[string]string{"web": "https://example.com/ag1"},
				},
			},
		})
	}))
	defer server.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:         server.URL,
		AccessToken: "test-access",
		IDToken:     "test-id",
	})

	result, err := proxyListAlertGroups(ctx, ListAlertGroupsParams{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "AG1", result[0].ID)
	assert.Equal(t, "new", result[0].State)
	assert.Equal(t, "Test Alert", result[0].Title)
	assert.Equal(t, 3, result[0].AlertsCount)
}

func TestProxyGetAlertGroup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/api/plugins/grafana-irm-app/resources/alertgroups/AG123/")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(onCallAlertGroupInternal{
			PK:          "AG123",
			AlertsCount: 1,
			Status:      float64(1),
			StartedAt:   "2025-02-01T08:00:00Z",
			RenderForWeb: &renderForWeb{
				Title: "Acknowledged Alert",
			},
		})
	}))
	defer server.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:         server.URL,
		AccessToken: "test-access",
		IDToken:     "test-id",
	})

	result, err := proxyGetAlertGroup(ctx, "AG123")
	require.NoError(t, err)
	assert.Equal(t, "AG123", result.ID)
	assert.Equal(t, "acknowledged", result.State)
	assert.Equal(t, "Acknowledged Alert", result.Title)
}

func TestProxyListSchedules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(paginatedResult[onCallScheduleInternal]{
			Results: []onCallScheduleInternal{
				{
					ID:       "SCHED1",
					Name:     "Primary Schedule",
					TimeZone: "UTC",
					Team:     "TEAM1",
					Shifts:   []string{"SHIFT1", "SHIFT2"},
				},
			},
		})
	}))
	defer server.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:         server.URL,
		AccessToken: "test-access",
		IDToken:     "test-id",
	})

	result, err := proxyListSchedules(ctx, ListOnCallSchedulesParams{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "SCHED1", result[0].ID)
	assert.Equal(t, "Primary Schedule", result[0].Name)
	assert.Equal(t, "UTC", result[0].Timezone)
	assert.Equal(t, "TEAM1", result[0].TeamID)
	assert.Equal(t, []string{"SHIFT1", "SHIFT2"}, result[0].Shifts)
}

func TestProxyPagination(t *testing.T) {
	page := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			nextURL := "https://oncall-prod.grafana.net/oncall/api/internal/v1/users/?page=2"
			_ = json.NewEncoder(w).Encode(paginatedResult[onCallUserInternal]{
				Results: []onCallUserInternal{{PK: "U1", Username: "user1"}},
				Next:    &nextURL,
			})
		} else {
			_ = json.NewEncoder(w).Encode(paginatedResult[onCallUserInternal]{
				Results: []onCallUserInternal{{PK: "U2", Username: "user2"}},
			})
		}
	}))
	defer server.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:         server.URL,
		AccessToken: "test-access",
		IDToken:     "test-id",
	})

	result, err := proxyListUsers(ctx, ListOnCallUsersParams{})
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "U1", result[0].ID)
	assert.Equal(t, "U2", result[1].ID)
}
