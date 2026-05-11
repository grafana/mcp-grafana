package tools

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManageAlertmanagerParams_Validate(t *testing.T) {
	tests := []struct {
		name     string
		readOnly bool
		params   ManageAlertmanagerParams
		wantErr  string
	}{
		{
			name:     "list alerts is valid in read-only mode",
			readOnly: true,
			params: ManageAlertmanagerParams{
				Operation:     "list_alerts",
				DatasourceUID: "alertmanager",
			},
		},
		{
			name:     "list silences is valid in read-only mode",
			readOnly: true,
			params: ManageAlertmanagerParams{
				Operation:     "list_silences",
				DatasourceUID: "alertmanager",
			},
		},
		{
			name:     "get silence requires id",
			readOnly: true,
			params: ManageAlertmanagerParams{
				Operation:     "get_silence",
				DatasourceUID: "alertmanager",
			},
			wantErr: "silence_id is required",
		},
		{
			name:     "create silence is blocked in read-only mode",
			readOnly: true,
			params: ManageAlertmanagerParams{
				Operation:     "create_or_update_silence",
				DatasourceUID: "alertmanager",
			},
			wantErr: "requires write tools",
		},
		{
			name: "create silence requires fields",
			params: ManageAlertmanagerParams{
				Operation:     "create_or_update_silence",
				DatasourceUID: "alertmanager",
				Matchers: []AlertmanagerMatcherParam{
					{Name: "alertname", Value: "HighCPU"},
				},
			},
			wantErr: "starts_at, ends_at, created_by, and comment are required",
		},
		{
			name: "create silence is valid in write mode",
			params: ManageAlertmanagerParams{
				Operation:     "create_or_update_silence",
				DatasourceUID: "alertmanager",
				Matchers: []AlertmanagerMatcherParam{
					{Name: "alertname", Value: "HighCPU"},
				},
				StartsAt:  "2026-04-24T00:00:00Z",
				EndsAt:    "2026-04-24T01:00:00Z",
				CreatedBy: "mcp-grafana",
				Comment:   "maintenance",
			},
		},
		{
			name: "delete silence requires id",
			params: ManageAlertmanagerParams{
				Operation:     "delete_silence",
				DatasourceUID: "alertmanager",
			},
			wantErr: "silence_id is required",
		},
		{
			name: "datasource uid is required",
			params: ManageAlertmanagerParams{
				Operation: "list_alerts",
			},
			wantErr: "datasource_uid is required",
		},
		{
			name: "unknown operation",
			params: ManageAlertmanagerParams{
				Operation:     "unknown",
				DatasourceUID: "alertmanager",
			},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.params.validate(tc.readOnly)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestManageAlertmanagerParams_QueryValues(t *testing.T) {
	falseValue := false
	params := ManageAlertmanagerParams{
		Matchers: []AlertmanagerMatcherParam{
			{Name: "alertname", Value: "HighCPU"},
			{Name: "namespace", Value: "prod|stage", IsRegex: true},
			{Name: "severity", Value: "info", IsEqual: &falseValue},
		},
		Active: &falseValue,
	}

	require.Equal(t, url.Values{
		"filter":      {`alertname="HighCPU"`, `namespace=~"prod|stage"`, `severity!="info"`},
		"active":      {"false"},
		"silenced":    {"false"},
		"inhibited":   {"false"},
		"unprocessed": {"false"},
	}, params.alertsQueryValues())
}

func TestManageAlertmanagerParams_ToPostableSilence(t *testing.T) {
	silence, err := ManageAlertmanagerParams{
		SilenceID: "abc123",
		Matchers: []AlertmanagerMatcherParam{
			{Name: "alertname", Value: "HighCPU"},
		},
		StartsAt:  "2026-04-24T00:00:00Z",
		EndsAt:    "2026-04-24T01:00:00Z",
		CreatedBy: "mcp-grafana",
		Comment:   "maintenance",
	}.toPostableSilence()

	require.NoError(t, err)
	require.Equal(t, "abc123", silence.ID)
	require.Equal(t, "mcp-grafana", *silence.CreatedBy)
	require.Equal(t, "maintenance", *silence.Comment)
	require.Len(t, silence.Matchers, 1)
	require.Equal(t, "alertname", *silence.Matchers[0].Name)
	require.Equal(t, "HighCPU", *silence.Matchers[0].Value)
	require.True(t, *silence.Matchers[0].IsEqual)
	require.False(t, *silence.Matchers[0].IsRegex)
}

func TestManageAlertmanagerParams_ToPostableSilenceRejectsInvalidTimeRange(t *testing.T) {
	_, err := ManageAlertmanagerParams{
		Matchers: []AlertmanagerMatcherParam{{Name: "alertname", Value: "HighCPU"}},
		StartsAt: "2026-04-24T01:00:00Z",
		EndsAt:   "2026-04-24T00:00:00Z",
	}.toPostableSilence()

	require.ErrorContains(t, err, "ends_at must be after starts_at")
}
