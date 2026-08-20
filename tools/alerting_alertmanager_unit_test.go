package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

// rfc3339 renders an offset from now, so tests exercising the "already
// expired" check stay valid as the clock moves.
func rfc3339(offset time.Duration) string {
	return time.Now().Add(offset).UTC().Format(time.RFC3339)
}

func TestManageAlertmanagerParams_Validate(t *testing.T) {
	validMatchers := []SilenceMatcherParam{{Name: "severity", Value: "critical"}}
	start := rfc3339(time.Hour)
	end := rfc3339(2 * time.Hour)
	comment := "maintenance window"

	tests := []struct {
		name    string
		params  ManageAlertmanagerParams
		wantErr string
	}{
		{
			name:   "list is valid with no params",
			params: ManageAlertmanagerParams{Operation: "list"},
		},
		{
			name:   "list_alert_groups is valid with no params",
			params: ManageAlertmanagerParams{Operation: "list_alert_groups"},
		},
		{
			name: "list_alert_groups with all filters is valid",
			params: ManageAlertmanagerParams{
				Operation: "list_alert_groups",
				Active:    boolPtr(true),
				Silenced:  boolPtr(false),
				Inhibited: boolPtr(false),
				Filter:    []string{"severity=critical"},
				Receiver:  "slack-ops",
			},
		},
		{
			name: "list with rule_uid filter is valid",
			params: ManageAlertmanagerParams{
				Operation: "list",
				RuleUID:   strPtr("abc123"),
			},
		},
		{
			name: "list with matcher filter is valid",
			params: ManageAlertmanagerParams{
				Operation: "list",
				Matchers:  validMatchers,
			},
		},
		{
			name: "get with silence_id is valid",
			params: ManageAlertmanagerParams{
				Operation: "get",
				SilenceID: strPtr("sil-1"),
			},
		},
		{
			name:    "get without silence_id",
			params:  ManageAlertmanagerParams{Operation: "get"},
			wantErr: "silence_id is required",
		},
		{
			name: "get with empty silence_id",
			params: ManageAlertmanagerParams{
				Operation: "get",
				SilenceID: strPtr(""),
			},
			wantErr: "silence_id is required",
		},
		{
			name: "create with all required fields is valid",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
		},
		{
			name: "create with silence_id is rejected",
			params: ManageAlertmanagerParams{
				Operation: "create",
				SilenceID: strPtr("abc123"),
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "silence_id must not be set for 'create'",
		},
		{
			name: "create with rule_uid is rejected",
			params: ManageAlertmanagerParams{
				Operation: "create",
				RuleUID:   strPtr("abc123"),
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "rule_uid must not be set for 'create'",
		},
		{
			name: "update with rule_uid is rejected",
			params: ManageAlertmanagerParams{
				Operation: "update",
				SilenceID: strPtr("sil-1"),
				RuleUID:   strPtr("abc123"),
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "rule_uid must not be set for 'update'",
		},
		{
			name: "create without matchers",
			params: ManageAlertmanagerParams{
				Operation: "create",
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "matchers is required",
		},
		{
			name: "create with empty matchers slice",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  []SilenceMatcherParam{},
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "matchers is required",
		},
		{
			name: "create with matcher missing name",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  []SilenceMatcherParam{{Value: "critical"}},
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "name is required",
		},
		{
			name: "create without starts_at",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "starts_at is required",
		},
		{
			name: "create without ends_at",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				Comment:   &comment,
			},
			wantErr: "ends_at is required",
		},
		{
			name: "create with invalid starts_at",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  strPtr("not-a-timestamp"),
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "starts_at must be a valid RFC3339 timestamp",
		},
		{
			name: "create with invalid ends_at",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    strPtr("2026-07-11 12:00"),
				Comment:   &comment,
			},
			wantErr: "ends_at must be a valid RFC3339 timestamp",
		},
		{
			name: "create without comment",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
			},
			wantErr: "comment is required",
		},
		{
			name: "create with empty comment",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   strPtr(""),
			},
			wantErr: "comment is required",
		},
		{
			name: "create with ends_at before starts_at",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &end,
				EndsAt:    &start,
				Comment:   &comment,
			},
			wantErr: "must be after starts_at",
		},
		{
			name: "create with a zero-length window",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &start,
				Comment:   &comment,
			},
			wantErr: "must be after starts_at",
		},
		{
			name: "create with a window entirely in the past",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  strPtr(rfc3339(-2 * time.Hour)),
				EndsAt:    strPtr(rfc3339(-time.Hour)),
				Comment:   &comment,
			},
			wantErr: "is in the past",
		},
		{
			name: "create starting in the past but ending in the future is valid",
			params: ManageAlertmanagerParams{
				Operation: "create",
				Matchers:  validMatchers,
				StartsAt:  strPtr(rfc3339(-time.Hour)),
				EndsAt:    &end,
				Comment:   &comment,
			},
		},
		{
			name: "update with all required fields is valid",
			params: ManageAlertmanagerParams{
				Operation: "update",
				SilenceID: strPtr("sil-1"),
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
		},
		{
			name: "update with a window that already closed points at delete",
			params: ManageAlertmanagerParams{
				Operation: "update",
				SilenceID: strPtr("sil-1"),
				Matchers:  validMatchers,
				StartsAt:  strPtr(rfc3339(-time.Hour)),
				EndsAt:    strPtr(rfc3339(-time.Minute)),
				Comment:   &comment,
			},
			wantErr: "use the 'delete' operation to expire a silence early",
		},
		{
			name: "update still rejects an inverted window",
			params: ManageAlertmanagerParams{
				Operation: "update",
				SilenceID: strPtr("sil-1"),
				Matchers:  validMatchers,
				StartsAt:  &end,
				EndsAt:    &start,
				Comment:   &comment,
			},
			wantErr: "must be after starts_at",
		},
		{
			name: "update without silence_id",
			params: ManageAlertmanagerParams{
				Operation: "update",
				Matchers:  validMatchers,
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "silence_id is required",
		},
		{
			name: "update without matchers still fails on payload",
			params: ManageAlertmanagerParams{
				Operation: "update",
				SilenceID: strPtr("sil-1"),
				StartsAt:  &start,
				EndsAt:    &end,
				Comment:   &comment,
			},
			wantErr: "matchers is required",
		},
		{
			name: "delete with silence_id is valid",
			params: ManageAlertmanagerParams{
				Operation: "delete",
				SilenceID: strPtr("sil-1"),
			},
		},
		{
			name:    "delete without silence_id",
			params:  ManageAlertmanagerParams{Operation: "delete"},
			wantErr: "silence_id is required",
		},
		{
			name:    "unknown operation",
			params:  ManageAlertmanagerParams{Operation: "expire"},
			wantErr: "unknown operation",
		},
		{
			name:    "empty operation",
			params:  ManageAlertmanagerParams{Operation: ""},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.validate()
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestManageAlertmanagerReadParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  ManageAlertmanagerReadParams
		wantErr string
	}{
		{
			name:   "list is valid",
			params: ManageAlertmanagerReadParams{Operation: "list"},
		},
		{
			name:   "list_alert_groups is valid",
			params: ManageAlertmanagerReadParams{Operation: "list_alert_groups"},
		},
		{
			name: "list_alert_groups with filters is valid",
			params: ManageAlertmanagerReadParams{
				Operation: "list_alert_groups",
				Active:    boolPtr(true),
				Filter:    []string{"severity=critical"},
				Receiver:  "oncall-slack",
			},
		},
		{
			name: "list with filters is valid",
			params: ManageAlertmanagerReadParams{
				Operation: "list",
				RuleUID:   strPtr("abc123"),
				Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical"}},
			},
		},
		{
			name: "get with silence_id is valid",
			params: ManageAlertmanagerReadParams{
				Operation: "get",
				SilenceID: strPtr("sil-1"),
			},
		},
		{
			name:    "get without silence_id",
			params:  ManageAlertmanagerReadParams{Operation: "get"},
			wantErr: "silence_id is required",
		},
		{
			name:    "create is rejected by read-only variant",
			params:  ManageAlertmanagerReadParams{Operation: "create"},
			wantErr: "unknown operation",
		},
		{
			name:    "delete is rejected by read-only variant",
			params:  ManageAlertmanagerReadParams{Operation: "delete"},
			wantErr: "unknown operation",
		},
		{
			name:    "empty operation",
			params:  ManageAlertmanagerReadParams{Operation: ""},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.validate()
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMatcherToFilterString(t *testing.T) {
	tests := []struct {
		name    string
		matcher SilenceMatcherParam
		want    string
	}{
		{
			name:    "equality (default isEqual)",
			matcher: SilenceMatcherParam{Name: "severity", Value: "critical"},
			want:    `severity="critical"`,
		},
		{
			name:    "explicit isEqual true",
			matcher: SilenceMatcherParam{Name: "severity", Value: "critical", IsEqual: boolPtr(true)},
			want:    `severity="critical"`,
		},
		{
			name:    "regex equality",
			matcher: SilenceMatcherParam{Name: "pod", Value: "api-.*", IsRegex: true},
			want:    `pod=~"api-.*"`,
		},
		{
			name:    "negative equality",
			matcher: SilenceMatcherParam{Name: "env", Value: "prod", IsEqual: boolPtr(false)},
			want:    `env!="prod"`,
		},
		{
			name:    "negative regex",
			matcher: SilenceMatcherParam{Name: "pod", Value: "api-.*", IsRegex: true, IsEqual: boolPtr(false)},
			want:    `pod!~"api-.*"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, matcherToFilterString(tc.matcher))
		})
	}
}

func TestBuildSilenceFilters(t *testing.T) {
	t.Run("no filters", func(t *testing.T) {
		require.Empty(t, buildSilenceFilters(nil, nil))
	})

	t.Run("rule_uid produces __alert_rule_uid__ matcher", func(t *testing.T) {
		got := buildSilenceFilters(strPtr("rule-xyz"), nil)
		require.Equal(t, []string{`__alert_rule_uid__="rule-xyz"`}, got)
	})

	t.Run("empty rule_uid is ignored", func(t *testing.T) {
		require.Empty(t, buildSilenceFilters(strPtr(""), nil))
	})

	t.Run("rule_uid and matchers combine in order", func(t *testing.T) {
		got := buildSilenceFilters(strPtr("rule-xyz"), []SilenceMatcherParam{
			{Name: "team", Value: "sre"},
		})
		require.Equal(t, []string{`__alert_rule_uid__="rule-xyz"`, `team="sre"`}, got)
	})
}

func TestToPostableSilence(t *testing.T) {
	start := "2026-07-11T10:00:00Z"
	end := "2026-07-11T12:00:00Z"
	comment := "maintenance"

	t.Run("create applies created_by default and no id", func(t *testing.T) {
		p := ManageAlertmanagerParams{
			Operation: "create",
			Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical", IsEqual: boolPtr(false)}},
			StartsAt:  &start,
			EndsAt:    &end,
			Comment:   &comment,
		}
		s, err := p.toPostableSilence()
		require.NoError(t, err)
		require.Empty(t, s.ID)
		require.Equal(t, defaultSilenceCreatedBy, *s.CreatedBy)
		require.Equal(t, comment, *s.Comment)
		require.True(t, time.Time(*s.StartsAt).Equal(mustParseRFC3339(t, start)))
		require.True(t, time.Time(*s.EndsAt).Equal(mustParseRFC3339(t, end)))
		require.Len(t, s.Matchers, 1)
		require.Equal(t, "severity", *s.Matchers[0].Name)
		require.Equal(t, "critical", *s.Matchers[0].Value)
		require.NotNil(t, s.Matchers[0].IsRegex)
		require.False(t, *s.Matchers[0].IsRegex)
		require.NotNil(t, s.Matchers[0].IsEqual)
		require.False(t, *s.Matchers[0].IsEqual)

		// created_by must be present in the outgoing JSON, not just the struct,
		// and the timestamps must survive the strfmt.DateTime round-trip.
		raw, err := json.Marshal(s)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.Equal(t, defaultSilenceCreatedBy, decoded["createdBy"])
		_, hasID := decoded["id"]
		require.False(t, hasID, "id should be omitted for create")

		gotStart, err := time.Parse(time.RFC3339, decoded["startsAt"].(string))
		require.NoError(t, err)
		require.True(t, gotStart.Equal(mustParseRFC3339(t, start)))
		gotEnd, err := time.Parse(time.RFC3339, decoded["endsAt"].(string))
		require.NoError(t, err)
		require.True(t, gotEnd.Equal(mustParseRFC3339(t, end)))
	})

	t.Run("explicit created_by is preserved", func(t *testing.T) {
		p := ManageAlertmanagerParams{
			Operation: "create",
			Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical"}},
			StartsAt:  &start,
			EndsAt:    &end,
			Comment:   &comment,
			CreatedBy: strPtr("alice"),
		}
		s, err := p.toPostableSilence()
		require.NoError(t, err)
		require.Equal(t, "alice", *s.CreatedBy)
	})

	t.Run("update carries silence id through", func(t *testing.T) {
		p := ManageAlertmanagerParams{
			Operation: "update",
			SilenceID: strPtr("sil-42"),
			Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical"}},
			StartsAt:  &start,
			EndsAt:    &end,
			Comment:   &comment,
		}
		s, err := p.toPostableSilence()
		require.NoError(t, err)
		require.Equal(t, "sil-42", s.ID)

		raw, err := json.Marshal(s)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.Equal(t, "sil-42", decoded["id"])
	})

	t.Run("unparseable timestamps surface an error", func(t *testing.T) {
		p := ManageAlertmanagerParams{
			Operation: "create",
			Matchers:  []SilenceMatcherParam{{Name: "severity", Value: "critical"}},
			StartsAt:  strPtr("not-a-timestamp"),
			EndsAt:    &end,
			Comment:   &comment,
		}
		_, err := p.toPostableSilence()
		require.ErrorContains(t, err, "starts_at must be a valid RFC3339 timestamp")
	})
}

func mustParseRFC3339(t *testing.T, v string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, v)
	require.NoError(t, err)
	return parsed
}

func TestGetAlertGroupsOpts_queryValues(t *testing.T) {
	tests := []struct {
		name     string
		opts     GetAlertGroupsOpts
		expected url.Values
	}{
		{
			name:     "empty opts produces empty values",
			opts:     GetAlertGroupsOpts{},
			expected: url.Values{},
		},
		{
			name: "active=true excludes other states",
			opts: GetAlertGroupsOpts{
				Active:    boolPtr(true),
				Silenced:  boolPtr(false),
				Inhibited: boolPtr(false),
			},
			expected: url.Values{
				"active":    {"true"},
				"silenced":  {"false"},
				"inhibited": {"false"},
			},
		},
		{
			name: "nil bools are omitted (API defaults apply)",
			opts: GetAlertGroupsOpts{
				Filter:   []string{"severity=critical"},
				Receiver: "team-slack",
			},
			expected: url.Values{
				"filter":   {"severity=critical"},
				"receiver": {"team-slack"},
			},
		},
		{
			name: "all fields populated",
			opts: GetAlertGroupsOpts{
				Active:    boolPtr(true),
				Silenced:  boolPtr(true),
				Inhibited: boolPtr(false),
				Filter:    []string{"severity=critical", "grafana_folder=Platform"},
				Receiver:  "oncall-slack",
			},
			expected: url.Values{
				"active":    {"true"},
				"silenced":  {"true"},
				"inhibited": {"false"},
				"filter":    {"severity=critical", "grafana_folder=Platform"},
				"receiver":  {"oncall-slack"},
			},
		},
		{
			name: "explicit false sent for all states",
			opts: GetAlertGroupsOpts{
				Active:    boolPtr(false),
				Silenced:  boolPtr(false),
				Inhibited: boolPtr(false),
			},
			expected: url.Values{
				"active":    {"false"},
				"silenced":  {"false"},
				"inhibited": {"false"},
			},
		},
		{
			name: "multiple filters",
			opts: GetAlertGroupsOpts{
				Filter: []string{"severity=critical", "alertname=HighCPU"},
			},
			expected: url.Values{
				"filter": {"severity=critical", "alertname=HighCPU"},
			},
		},
		{
			name: "receiver only",
			opts: GetAlertGroupsOpts{
				Receiver: "slack-ops",
			},
			expected: url.Values{
				"receiver": {"slack-ops"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.queryValues()
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestAlertingClient_GetAlertGroups(t *testing.T) {
	mockAlertGroup := &models.AlertGroup{
		Labels:   models.LabelSet{"alertname": "HighCPU", "grafana_folder": "Production"},
		Receiver: &models.Receiver{Name: strPtr("slack-ops")},
		Alerts: []*models.GettableAlert{
			{
				Alert: models.Alert{
					Labels: models.LabelSet{"instance": "server-1"},
				},
				Annotations: models.LabelSet{
					"summary":     "CPU usage high",
					"description": "CPU usage above 90% on server-1",
				},
				Status: &models.AlertStatus{State: strPtr("active")},
			},
		},
	}

	t.Run("success without opts", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/alertmanager/grafana/api/v2/alerts/groups", r.URL.Path)
			require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
			require.Empty(t, r.URL.RawQuery)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode([]*models.AlertGroup{mockAlertGroup})
			require.NoError(t, err)
		})
		defer server.Close()

		groups, err := client.GetAlertGroups(context.Background(), nil)
		require.NoError(t, err)
		require.Len(t, groups, 1)
		require.Equal(t, "HighCPU", groups[0].Labels["alertname"])
	})

	t.Run("success with opts", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/alertmanager/grafana/api/v2/alerts/groups", r.URL.Path)
			q := r.URL.Query()
			require.Equal(t, "true", q.Get("active"))
			require.Equal(t, "false", q.Get("silenced"))
			require.Equal(t, "false", q.Get("inhibited"))
			require.Equal(t, "slack-ops", q.Get("receiver"))
			require.Equal(t, []string{"severity=critical"}, q["filter"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode([]*models.AlertGroup{mockAlertGroup})
			require.NoError(t, err)
		})
		defer server.Close()

		opts := &GetAlertGroupsOpts{
			Active:    boolPtr(true),
			Silenced:  boolPtr(false),
			Inhibited: boolPtr(false),
			Filter:    []string{"severity=critical"},
			Receiver:  "slack-ops",
		}
		groups, err := client.GetAlertGroups(context.Background(), opts)
		require.NoError(t, err)
		require.Len(t, groups, 1)
	})

	t.Run("server error", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte("internal error"))
			require.NoError(t, err)
		})
		defer server.Close()

		groups, err := client.GetAlertGroups(context.Background(), nil)
		require.Error(t, err)
		require.Nil(t, groups)
		require.ErrorContains(t, err, "500")
	})

	t.Run("network error", func(t *testing.T) {
		server, client := setupMockServer(func(w http.ResponseWriter, r *http.Request) {})
		server.Close()

		groups, err := client.GetAlertGroups(context.Background(), nil)
		require.Error(t, err)
		require.Nil(t, groups)
		require.ErrorContains(t, err, "failed to execute request")
	})
}
