package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// Unit tests for parameter validation (no integration tag needed)
func TestCreateAlertRuleParams_Validate(t *testing.T) {
	t.Run("valid parameters", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.NoError(t, err)
	})

	t.Run("missing title", func(t *testing.T) {
		params := CreateAlertRuleParams{
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "title is required")
	})

	t.Run("missing rule group", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "rule_group is required")
	})

	t.Run("missing folder UID", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "folder_uid is required")
	})

	t.Run("missing condition", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "condition is required")
	})

	t.Run("missing data", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "data is required")
	})

	t.Run("missing no data state", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "no_data_state is required")
	})

	t.Run("missing exec error state", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:       "Test Rule",
			RuleGroup:   "test-group",
			FolderUID:   "test-folder",
			Condition:   "A",
			Data:        []*AlertQuery{{RefID: "A"}},
			NoDataState: "OK",
			For:         "5m",
			OrgID:       1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "exec_err_state is required")
	})

	t.Run("missing for duration", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "for duration is required")
	})

	t.Run("invalid org ID", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        0,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "org_id is required and must be greater than 0")
	})

	t.Run("with disableProvenance true", func(t *testing.T) {
		disableProvenance := true
		params := CreateAlertRuleParams{
			Title:             "Test Rule",
			RuleGroup:         "test-group",
			FolderUID:         "test-folder",
			Condition:         "A",
			Data:              []*AlertQuery{{RefID: "A"}},
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		}
		err := params.validate()
		require.NoError(t, err)
	})

	t.Run("with disableProvenance false", func(t *testing.T) {
		disableProvenance := false
		params := CreateAlertRuleParams{
			Title:             "Test Rule",
			RuleGroup:         "test-group",
			FolderUID:         "test-folder",
			Condition:         "A",
			Data:              []*AlertQuery{{RefID: "A"}},
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		}
		err := params.validate()
		require.NoError(t, err)
	})

	t.Run("with disableProvenance nil (default)", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:             "Test Rule",
			RuleGroup:         "test-group",
			FolderUID:         "test-folder",
			Condition:         "A",
			Data:              []*AlertQuery{{RefID: "A"}},
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: nil,
		}
		err := params.validate()
		require.NoError(t, err)
	})
}

func TestUpdateAlertRuleParams_Validate(t *testing.T) {
	t.Run("valid parameters", func(t *testing.T) {
		params := UpdateAlertRuleParams{
			UID:          "test-uid",
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.NoError(t, err)
	})

	t.Run("missing UID", func(t *testing.T) {
		params := UpdateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "rule_uid is required")
	})

	t.Run("invalid org ID", func(t *testing.T) {
		params := UpdateAlertRuleParams{
			UID:          "test-uid",
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        -1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "org_id is required and must be greater than 0")
	})

	t.Run("with disableProvenance true", func(t *testing.T) {
		disableProvenance := true
		params := UpdateAlertRuleParams{
			UID:               "test-uid",
			Title:             "Test Rule",
			RuleGroup:         "test-group",
			FolderUID:         "test-folder",
			Condition:         "A",
			Data:              []*AlertQuery{{RefID: "A"}},
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		}
		err := params.validate()
		require.NoError(t, err)
	})

	t.Run("with disableProvenance false", func(t *testing.T) {
		disableProvenance := false
		params := UpdateAlertRuleParams{
			UID:               "test-uid",
			Title:             "Test Rule",
			RuleGroup:         "test-group",
			FolderUID:         "test-folder",
			Condition:         "A",
			Data:              []*AlertQuery{{RefID: "A"}},
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		}
		err := params.validate()
		require.NoError(t, err)
	})

	t.Run("with disableProvenance nil (default)", func(t *testing.T) {
		params := UpdateAlertRuleParams{
			UID:               "test-uid",
			Title:             "Test Rule",
			RuleGroup:         "test-group",
			FolderUID:         "test-folder",
			Condition:         "A",
			Data:              []*AlertQuery{{RefID: "A"}},
			NoDataState:       "OK",
			ExecErrState:      "OK",
			For:               "5m",
			OrgID:             1,
			DisableProvenance: nil,
		}
		err := params.validate()
		require.NoError(t, err)
	})
}

func TestDeleteAlertRuleParams_Validate(t *testing.T) {
	t.Run("valid parameters", func(t *testing.T) {
		params := DeleteAlertRuleParams{
			UID: "test-uid",
		}
		err := params.validate()
		require.NoError(t, err)
	})

	t.Run("missing UID", func(t *testing.T) {
		params := DeleteAlertRuleParams{
			UID: "",
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "uid is required")
	})
}

func TestBuiltInValidationCatchesInvalidData(t *testing.T) {
	t.Run("invalid NoDataState enum value", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "InvalidValue", // Invalid enum
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}

		// Our simple validation won't catch this, but it would fail at API call
		err := params.validate()
		require.NoError(t, err, "Simple validation doesn't check enum values")
	})

	t.Run("invalid ExecErrState enum value", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "BadValue", // Invalid enum
			For:          "5m",
			OrgID:        1,
		}

		// Our simple validation won't catch this
		err := params.validate()
		require.NoError(t, err, "Simple validation doesn't check enum values")
	})

	t.Run("title too long", func(t *testing.T) {
		longTitle := make([]byte, 200) // Max is 190
		for i := range longTitle {
			longTitle[i] = 'A'
		}

		params := CreateAlertRuleParams{
			Title:        string(longTitle),
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []*AlertQuery{{RefID: "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}

		// Simple validation only checks if title is empty, not length
		err := params.validate()
		require.NoError(t, err, "Simple validation doesn't check length constraints")
	})
}
func TestRecord_Validate(t *testing.T) {
	from := "A"
	metric := "my_metric"

	t.Run("valid record", func(t *testing.T) {
		r := &Record{From: &from, Metric: &metric}
		require.NoError(t, r.validate())
	})

	t.Run("nil From", func(t *testing.T) {
		r := &Record{From: nil, Metric: &metric}
		err := r.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "record.from is required")
	})

	t.Run("empty From", func(t *testing.T) {
		empty := ""
		r := &Record{From: &empty, Metric: &metric}
		err := r.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "record.from is required")
	})

	t.Run("nil Metric", func(t *testing.T) {
		r := &Record{From: &from, Metric: nil}
		err := r.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "record.metric is required")
	})

	t.Run("empty Metric", func(t *testing.T) {
		empty := ""
		r := &Record{From: &from, Metric: &empty}
		err := r.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "record.metric is required")
	})
}

func setupManageRulesTestContext(t *testing.T, assertRequest func(t *testing.T, r *http.Request)) context.Context {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if assertRequest != nil {
			assertRequest(t, r)
		}
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(mockrulesResponse())
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	return mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "test-api-key",
	})
}

var expectedMockRuleSummary = alertRuleSummary{
	UID:       "test-rule-uid",
	Title:     "Test Alert Rule",
	State:     "firing",
	Health:    "",
	FolderUID: "test-folder",
	RuleGroup: "TestGroup",
	Labels:    map[string]string{"severity": "critical"},
}

func TestManageRules_ListRules(t *testing.T) {
	tests := []struct {
		name          string
		params        ManageRulesReadParams
		assertRequest func(t *testing.T, r *http.Request)
		wantErr       string
		expectedRules []alertRuleSummary
	}{
		// Validation errors (mock server is never hit)
		{
			name:    "negative rule_limit",
			params:  ManageRulesReadParams{listFilterParams: listFilterParams{RuleLimit: -1}, Operation: "list"},
			wantErr: "invalid rule_limit",
		},
		{
			name:    "folder_uid and search_folder mutually exclusive",
			params:  ManageRulesReadParams{listFilterParams: listFilterParams{SearchFolder: "Production"}, Operation: "list", FolderUID: "folder-1"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "invalid matcher type",
			params:  ManageRulesReadParams{listFilterParams: listFilterParams{Matchers: []LabelMatcher{{Name: "severity", Type: ">>", Value: "critical"}}}, Operation: "list"},
			wantErr: "invalid matcher type",
		},
		{
			name:    "invalid regex matcher value",
			params:  ManageRulesReadParams{listFilterParams: listFilterParams{Matchers: []LabelMatcher{{Name: "severity", Type: "=~", Value: "[invalid"}}}, Operation: "list"},
			wantErr: "invalid matcher",
		},
		{
			name:    "get without rule_uid",
			params:  ManageRulesReadParams{Operation: "get"},
			wantErr: "rule_uid is required",
		},
		{
			name:    "unknown operation",
			params:  ManageRulesReadParams{Operation: "create"},
			wantErr: "unknown operation",
		},
		// Successful list with query params forwarded
		{
			name:   "list with defaults",
			params: ManageRulesReadParams{Operation: "list"},
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				require.Equal(t, "/api/prometheus/grafana/api/v1/rules", r.URL.Path)
			},
			expectedRules: []alertRuleSummary{expectedMockRuleSummary},
		},
		{
			name: "list with folder_uid",
			params: ManageRulesReadParams{
				Operation: "list",
				FolderUID: "test-folder",
			},
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				require.Equal(t, "test-folder", r.URL.Query().Get("folder_uid"))
			},
			expectedRules: []alertRuleSummary{expectedMockRuleSummary},
		},
		{
			name: "list with all filter params",
			params: ManageRulesReadParams{
				listFilterParams: listFilterParams{
					RuleLimit:      10,
					SearchRuleName: "cpu",
					RuleType:       "alerting",
					States:         []string{"firing"},
					Matchers:       []LabelMatcher{{Name: "severity", Type: "=", Value: "critical"}},
				},
				Operation: "list",
				FolderUID: "test-folder",
				RuleGroup: "test-group",
			},
			assertRequest: func(t *testing.T, r *http.Request) {
				t.Helper()
				q := r.URL.Query()
				require.Equal(t, "test-folder", q.Get("folder_uid"))
				require.Equal(t, "test-group", q.Get("rule_group"))
				require.Equal(t, "cpu", q.Get("search.rule_name"))
				require.Equal(t, "alerting", q.Get("rule_type"))
				require.Equal(t, []string{"firing"}, q["state"])
				require.Equal(t, "10", q.Get("rule_limit"))
				require.NotEmpty(t, q.Get("matcher"))
			},
			expectedRules: []alertRuleSummary{expectedMockRuleSummary},
		},
		{
			name: "list with label selector filters matching rule",
			params: ManageRulesReadParams{
				listFilterParams: listFilterParams{
					LabelSelectors: []Selector{{Filters: []LabelMatcher{{Name: "severity", Type: "=", Value: "critical"}}}},
				},
				Operation: "list",
			},
			expectedRules: []alertRuleSummary{expectedMockRuleSummary},
		},
		{
			name: "list with label selector filters not matching",
			params: ManageRulesReadParams{
				listFilterParams: listFilterParams{
					LabelSelectors: []Selector{{Filters: []LabelMatcher{{Name: "severity", Type: "=", Value: "warning"}}}},
				},
				Operation: "list",
			},
			expectedRules: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := setupManageRulesTestContext(t, tc.assertRequest)
			result, err := manageRulesRead(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			rules, ok := result.([]alertRuleSummary)
			require.True(t, ok)
			require.Equal(t, tc.expectedRules, rules)
		})
	}
}

func TestManageRulesReadWrite_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		call    func() (any, error)
		wantErr string
	}{
		{
			name: "negative rule_limit",
			call: func() (any, error) {
				return manageRulesReadWrite(ctx, ManageRulesReadWriteParams{listFilterParams: listFilterParams{RuleLimit: -1}, Operation: "list"})
			},
			wantErr: "invalid rule_limit",
		},
		{
			name: "folder_uid and search_folder mutually exclusive",
			call: func() (any, error) {
				return manageRulesReadWrite(ctx, ManageRulesReadWriteParams{listFilterParams: listFilterParams{SearchFolder: "Production"}, Operation: "list", FolderUID: "folder-1"})
			},
			wantErr: "mutually exclusive",
		},
		{
			name: "create missing title",
			call: func() (any, error) {
				return manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
					Operation: "create", RuleGroup: "test-group", FolderUID: "test-folder",
					Condition: "A", Data: []*AlertQuery{{RefID: "A"}},
					NoDataState: "OK", ExecErrState: "OK", For: "5m", OrgID: 1,
				})
			},
			wantErr: "title is required",
		},
		{
			name: "update missing rule_uid",
			call: func() (any, error) {
				return manageRulesReadWrite(ctx, ManageRulesReadWriteParams{
					Operation: "update", Title: "Test Rule", RuleGroup: "test-group", FolderUID: "test-folder",
					Condition: "A", Data: []*AlertQuery{{RefID: "A"}},
					NoDataState: "OK", ExecErrState: "OK", For: "5m", OrgID: 1,
				})
			},
			wantErr: "rule_uid is required",
		},
		{
			name:    "delete without rule_uid",
			call:    func() (any, error) { return manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "delete"}) },
			wantErr: "rule_uid is required",
		},
		{
			name: "unknown operation",
			call: func() (any, error) {
				return manageRulesReadWrite(ctx, ManageRulesReadWriteParams{Operation: "invalid"})
			},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.call()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestManageRulesReadWriteParams_ToCreateParams(t *testing.T) {
	t.Run("converts all fields", func(t *testing.T) {
		disableProvenance := true
		params := ManageRulesReadWriteParams{
			Operation:         "create",
			RuleUID:           "custom-uid",
			Title:             "Test Rule",
			RuleGroup:         "test-group",
			FolderUID:         "test-folder",
			Condition:         "B",
			Data:              []*AlertQuery{{RefID: "A"}, {RefID: "B"}},
			NoDataState:       "Alerting",
			ExecErrState:      "OK",
			For:               "10m",
			Annotations:       map[string]string{"summary": "test"},
			Labels:            map[string]string{"team": "backend"},
			OrgID:             1,
			DisableProvenance: &disableProvenance,
		}

		result := params.toCreateParams()
		require.Equal(t, "Test Rule", result.Title)
		require.Equal(t, "test-group", result.RuleGroup)
		require.Equal(t, "test-folder", result.FolderUID)
		require.Equal(t, "B", result.Condition)
		require.Len(t, result.Data, 2)
		require.Equal(t, "Alerting", result.NoDataState)
		require.Equal(t, "OK", result.ExecErrState)
		require.Equal(t, "10m", result.For)
		require.Equal(t, map[string]string{"summary": "test"}, result.Annotations)
		require.Equal(t, map[string]string{"team": "backend"}, result.Labels)
		require.Equal(t, int64(1), result.OrgID)
		require.NotNil(t, result.UID)
		require.Equal(t, "custom-uid", *result.UID)
		require.NotNil(t, result.DisableProvenance)
		require.True(t, *result.DisableProvenance)
	})

	t.Run("empty rule_uid results in nil UID", func(t *testing.T) {
		params := ManageRulesReadWriteParams{
			Operation: "create",
			Title:     "Test Rule",
		}

		result := params.toCreateParams()
		require.Nil(t, result.UID)
	})
}

func TestManageRulesReadWriteParams_ToUpdateParams(t *testing.T) {
	t.Run("converts all fields", func(t *testing.T) {
		disableProvenance := false
		params := ManageRulesReadWriteParams{
			Operation:         "update",
			RuleUID:           "rule-uid-123",
			Title:             "Updated Rule",
			RuleGroup:         "updated-group",
			FolderUID:         "updated-folder",
			Condition:         "A",
			Data:              []*AlertQuery{{RefID: "A"}},
			NoDataState:       "NoData",
			ExecErrState:      "Alerting",
			For:               "15m",
			Annotations:       map[string]string{"description": "updated"},
			Labels:            map[string]string{"env": "prod"},
			OrgID:             2,
			DisableProvenance: &disableProvenance,
		}

		result := params.toUpdateParams()
		require.Equal(t, "rule-uid-123", result.UID)
		require.Equal(t, "Updated Rule", result.Title)
		require.Equal(t, "updated-group", result.RuleGroup)
		require.Equal(t, "updated-folder", result.FolderUID)
		require.Equal(t, "A", result.Condition)
		require.Len(t, result.Data, 1)
		require.Equal(t, "NoData", result.NoDataState)
		require.Equal(t, "Alerting", result.ExecErrState)
		require.Equal(t, "15m", result.For)
		require.Equal(t, map[string]string{"description": "updated"}, result.Annotations)
		require.Equal(t, map[string]string{"env": "prod"}, result.Labels)
		require.Equal(t, int64(2), result.OrgID)
		require.NotNil(t, result.DisableProvenance)
		require.False(t, *result.DisableProvenance)
	})
}

func TestMergeRuleDetail(t *testing.T) {
	t.Run("merges provisioned config with runtime state", func(t *testing.T) {
		title := "High CPU Alert"
		folderUID := "folder-1"
		ruleGroup := "infra"
		condition := "A"
		noDataState := "OK"
		execErrState := "Alerting"
		forDuration := strfmt.Duration(5 * time.Minute)

		receiver := "slack-notifications"
		provisioned := &models.ProvisionedAlertRule{
			UID:          "rule-123",
			Title:        &title,
			FolderUID:    &folderUID,
			RuleGroup:    &ruleGroup,
			Condition:    &condition,
			NoDataState:  &noDataState,
			ExecErrState: &execErrState,
			For:          &forDuration,
			Labels:       map[string]string{"severity": "critical"},
			Annotations:  map[string]string{"summary": "CPU is high"},
			IsPaused:     true,
			NotificationSettings: &models.AlertRuleNotificationSettings{
				Receiver: &receiver,
			},
			Data: []*models.AlertQuery{
				{
					RefID:         "A",
					DatasourceUID: "prometheus-uid",
					Model:         map[string]any{"expr": "up{job=\"api\"}"},
				},
				{
					RefID:         "B",
					DatasourceUID: "__expr__",
					Model:         map[string]any{"expression": "$A > 0", "type": "math"},
				},
			},
		}

		evalTime := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
		runtime := &alertingRule{
			State:          "firing",
			Health:         "ok",
			Type:           "alerting",
			LastEvaluation: evalTime,
			LastError:      "some transient error",
			Alerts: []alert{
				{
					Labels: labels.New(labels.Label{Name: "instance", Value: "server-1"}),
					State:  "firing",
					Value:  "95.2",
				},
			},
		}

		detail := mergeRuleDetail(provisioned, runtime)

		require.Equal(t, "rule-123", detail.UID)
		require.Equal(t, "High CPU Alert", detail.Title)
		require.Equal(t, "folder-1", detail.FolderUID)
		require.Equal(t, "infra", detail.RuleGroup)
		require.Equal(t, "A", detail.Condition)
		require.Equal(t, "OK", detail.NoDataState)
		require.Equal(t, "Alerting", detail.ExecErrState)
		require.Equal(t, "5m0s", detail.For)
		require.Equal(t, map[string]string{"severity": "critical"}, detail.Labels)
		require.Equal(t, map[string]string{"summary": "CPU is high"}, detail.Annotations)

		// New provisioned fields
		require.True(t, detail.IsPaused)
		require.NotNil(t, detail.NotificationSettings)
		require.Equal(t, "slack-notifications", *detail.NotificationSettings.Receiver)
		require.Len(t, detail.Queries, 2)
		require.Equal(t, "A", detail.Queries[0].RefID)
		require.Equal(t, "prometheus-uid", detail.Queries[0].DatasourceUID)
		require.Equal(t, "up{job=\"api\"}", detail.Queries[0].Expression)
		require.Equal(t, "B", detail.Queries[1].RefID)
		require.Equal(t, "__expr__", detail.Queries[1].DatasourceUID)
		require.Equal(t, "$A > 0", detail.Queries[1].Expression)

		// Runtime fields
		require.Equal(t, "firing", detail.State)
		require.Equal(t, "ok", detail.Health)
		require.Equal(t, "alerting", detail.Type)
		require.Equal(t, "2026-02-28T12:00:00Z", detail.LastEvaluation)
		require.Equal(t, "some transient error", detail.LastError)
		require.Len(t, detail.Alerts, 1)
		require.Equal(t, "firing", detail.Alerts[0].State)
	})

	t.Run("merges provisioned recording rule config with runtime state", func(t *testing.T) {
		title := "High CPU Recording"
		folderUID := "folder-1"
		ruleGroup := "infra"
		condition := "A"

		from := "A"
		metric := "cpu_usage_avg"
		record := &models.Record{
			From:   &from,
			Metric: &metric,
		}

		provisioned := &models.ProvisionedAlertRule{
			UID:       "record-123",
			Title:     &title,
			FolderUID: &folderUID,
			RuleGroup: &ruleGroup,
			Condition: &condition,
			Record:    record,
			Data: []*models.AlertQuery{
				{
					RefID:         "A",
					DatasourceUID: "prometheus-uid",
					Model:         map[string]any{"expr": "avg(up{job=\"api\"})"},
				},
			},
		}

		evalTime := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
		runtime := &alertingRule{
			State:          "inactive",
			Health:         "ok",
			Type:           "recording",
			LastEvaluation: evalTime,
		}

		detail := mergeRuleDetail(provisioned, runtime)
		t.Logf("detail: %+v", detail)

		require.Equal(t, "record-123", detail.UID, "the rule UID should match")
		require.Equal(t, "High CPU Recording", detail.Title, "the rule title should match")
		require.Equal(t, "folder-1", detail.FolderUID, "the folder UID should match")
		require.Equal(t, "infra", detail.RuleGroup, "the rule group name should match")
		require.NotNil(t, detail.Record, "the recording configuration should not be nil")
		require.Equal(t, "A", *detail.Record.From, "the from identifier in recording config should match")
		require.Equal(t, "cpu_usage_avg", *detail.Record.Metric, "the target metric name in recording config should match")

		require.Len(t, detail.Queries, 1, "the queries slice should have length 1")
		require.Equal(t, "A", detail.Queries[0].RefID, "the query refID should match")
		require.Equal(t, "prometheus-uid", detail.Queries[0].DatasourceUID, "the query datasource UID should match")
		require.Equal(t, "avg(up{job=\"api\"})", detail.Queries[0].Expression, "the query expression should match")

		// Runtime fields
		require.Equal(t, "normal", detail.State, "the rule state should be normalized from inactive to normal")
		require.Equal(t, "ok", detail.Health, "the health status should match")
		require.Equal(t, "recording", detail.Type, "the rule type should be 'recording'")
		require.Equal(t, "2026-02-28T12:00:00Z", detail.LastEvaluation, "the last evaluation time should match formatted UTC time")
	})

	t.Run("nil runtime leaves state fields empty", func(t *testing.T) {
		title := "Disk Alert"
		provisioned := &models.ProvisionedAlertRule{
			UID:   "rule-456",
			Title: &title,
		}

		detail := mergeRuleDetail(provisioned, nil)

		require.Equal(t, "rule-456", detail.UID)
		require.Equal(t, "Disk Alert", detail.Title)
		require.Empty(t, detail.State)
		require.Empty(t, detail.Health)
		require.Empty(t, detail.Type)
		require.Empty(t, detail.LastEvaluation)
		require.Empty(t, detail.LastError)
		require.Nil(t, detail.Alerts)
		require.False(t, detail.IsPaused)
		require.Nil(t, detail.NotificationSettings)
		require.Nil(t, detail.Queries)
	})

	t.Run("provisioned with nil pointer fields", func(t *testing.T) {
		provisioned := &models.ProvisionedAlertRule{
			UID: "rule-789",
		}

		detail := mergeRuleDetail(provisioned, nil)

		require.Equal(t, "rule-789", detail.UID)
		require.Empty(t, detail.Title)
		require.Empty(t, detail.FolderUID)
		require.Empty(t, detail.RuleGroup)
		require.Empty(t, detail.Condition)
		require.Empty(t, detail.NoDataState)
		require.Empty(t, detail.ExecErrState)
		require.Empty(t, detail.For)
	})

	t.Run("inactive state is normalized to normal", func(t *testing.T) {
		title := "Quiet Rule"
		provisioned := &models.ProvisionedAlertRule{
			UID:   "rule-quiet",
			Title: &title,
		}

		evalTime := time.Date(2026, 2, 28, 10, 30, 0, 0, time.UTC)
		runtime := &alertingRule{
			State:          "inactive",
			Health:         "ok",
			Type:           "alerting",
			LastEvaluation: evalTime,
			Alerts:         nil,
		}

		detail := mergeRuleDetail(provisioned, runtime)

		require.Equal(t, "normal", detail.State, "inactive should be normalized to normal")
		require.Equal(t, "ok", detail.Health)
		require.Equal(t, "alerting", detail.Type)
		require.Equal(t, "2026-02-28T10:30:00Z", detail.LastEvaluation)
		require.Empty(t, detail.LastError)
		require.Nil(t, detail.Alerts)
	})
}

func TestConvertRulesResponseToSummary(t *testing.T) {
	evalTime := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)

	t.Run("converts multiple groups and rules", func(t *testing.T) {
		resp := &rulesResponse{}
		resp.Data.RuleGroups = []ruleGroup{
			{
				Name:      "group-1",
				FolderUID: "folder-a",
				Rules: []alertingRule{
					{
						UID:            "uid-1",
						Name:           "High CPU",
						State:          "firing",
						Health:         "ok",
						LastEvaluation: evalTime,
						Duration:       300, // 5m
						Labels:         labels.FromStrings("severity", "critical"),
						Annotations:    labels.FromStrings("summary", "CPU is high"),
					},
					{
						UID:            "uid-2",
						Name:           "Low Memory",
						State:          "inactive",
						Health:         "ok",
						LastEvaluation: evalTime,
					},
				},
			},
			{
				Name:      "group-2",
				FolderUID: "folder-b",
				Rules: []alertingRule{
					{
						UID:            "uid-3",
						Name:           "Disk Full",
						State:          "pending",
						Health:         "ok",
						LastEvaluation: evalTime,
						Duration:       600, // 10m
					},
				},
			},
		}

		summaries := convertRulesResponseToSummary(resp)
		require.Len(t, summaries, 3)

		require.Equal(t, "uid-1", summaries[0].UID)
		require.Equal(t, "High CPU", summaries[0].Title)
		require.Equal(t, "firing", summaries[0].State)
		require.Equal(t, "ok", summaries[0].Health)
		require.Equal(t, "folder-a", summaries[0].FolderUID)
		require.Equal(t, "group-1", summaries[0].RuleGroup)
		require.Equal(t, "5m0s", summaries[0].For)
		require.Equal(t, "2026-02-28T12:00:00Z", summaries[0].LastEvaluation)
		require.Equal(t, map[string]string{"severity": "critical"}, summaries[0].Labels)
		require.Equal(t, map[string]string{"summary": "CPU is high"}, summaries[0].Annotations)

		require.Equal(t, "uid-2", summaries[1].UID)
		require.Equal(t, "Low Memory", summaries[1].Title)
		require.Equal(t, "normal", summaries[1].State)
		require.Equal(t, "folder-a", summaries[1].FolderUID)
		require.Equal(t, "group-1", summaries[1].RuleGroup)
		require.Empty(t, summaries[1].For)
		require.Nil(t, summaries[1].Labels)
		require.Nil(t, summaries[1].Annotations)

		require.Equal(t, "uid-3", summaries[2].UID)
		require.Equal(t, "Disk Full", summaries[2].Title)
		require.Equal(t, "pending", summaries[2].State)
		require.Equal(t, "folder-b", summaries[2].FolderUID)
		require.Equal(t, "group-2", summaries[2].RuleGroup)
		require.Equal(t, "10m0s", summaries[2].For)
	})

	t.Run("empty response returns nil", func(t *testing.T) {
		resp := &rulesResponse{}
		summaries := convertRulesResponseToSummary(resp)
		require.Nil(t, summaries)
	})

	t.Run("zero last evaluation is omitted", func(t *testing.T) {
		resp := &rulesResponse{}
		resp.Data.RuleGroups = []ruleGroup{
			{
				Name:      "g",
				FolderUID: "f",
				Rules: []alertingRule{
					{UID: "uid-4", Name: "Zero Eval", State: "inactive", Health: "ok"},
				},
			},
		}

		summaries := convertRulesResponseToSummary(resp)
		require.Len(t, summaries, 1)
		require.Empty(t, summaries[0].LastEvaluation)
	})
}

func TestFilterSummaryByLabels(t *testing.T) {
	summaries := []alertRuleSummary{
		{UID: "r1", Title: "Rule 1", Labels: map[string]string{"severity": "critical", "team": "backend"}},
		{UID: "r2", Title: "Rule 2", Labels: map[string]string{"severity": "warning", "team": "frontend"}},
		{UID: "r3", Title: "Rule 3", Labels: map[string]string{"severity": "critical", "team": "frontend"}},
		{UID: "r4", Title: "Rule 4"},
	}

	t.Run("no selectors returns all", func(t *testing.T) {
		filtered, err := filterSummaryByLabels(summaries, nil)
		require.NoError(t, err)
		require.Len(t, filtered, 4)
	})

	t.Run("filter by single label", func(t *testing.T) {
		filtered, err := filterSummaryByLabels(summaries, []Selector{
			{Filters: []LabelMatcher{{Name: "severity", Type: "=", Value: "critical"}}},
		})
		require.NoError(t, err)
		require.Len(t, filtered, 2)
		require.Equal(t, "r1", filtered[0].UID)
		require.Equal(t, "r3", filtered[1].UID)
	})

	t.Run("filter by multiple labels", func(t *testing.T) {
		filtered, err := filterSummaryByLabels(summaries, []Selector{
			{Filters: []LabelMatcher{
				{Name: "severity", Type: "=", Value: "critical"},
				{Name: "team", Type: "=", Value: "backend"},
			}},
		})
		require.NoError(t, err)
		require.Len(t, filtered, 1)
		require.Equal(t, "r1", filtered[0].UID)
	})

	t.Run("nil labels are treated as empty map", func(t *testing.T) {
		filtered, err := filterSummaryByLabels(summaries, []Selector{
			{Filters: []LabelMatcher{{Name: "severity", Type: "=", Value: "critical"}}},
		})
		require.NoError(t, err)
		// r4 has nil labels, should not match
		for _, s := range filtered {
			require.NotEqual(t, "r4", s.UID)
		}
	})
}

func TestFindRuleInResponse(t *testing.T) {
	evalTime := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)

	resp := &rulesResponse{}
	resp.Data.RuleGroups = []ruleGroup{
		{
			Name:      "group-1",
			FolderUID: "folder-a",
			Rules: []alertingRule{
				{
					UID:            "uid-1",
					Name:           "Rule One",
					State:          "firing",
					Health:         "ok",
					LastEvaluation: evalTime,
				},
				{
					UID:            "uid-2",
					Name:           "Rule Two",
					State:          "inactive",
					Health:         "ok",
					LastEvaluation: evalTime,
				},
			},
		},
		{
			Name:      "group-2",
			FolderUID: "folder-b",
			Rules: []alertingRule{
				{
					UID:            "uid-3",
					Name:           "Rule Three",
					State:          "pending",
					Health:         "ok",
					LastEvaluation: evalTime,
				},
			},
		},
	}

	t.Run("finds rule in first group", func(t *testing.T) {
		rule := findRuleInResponse(resp, "uid-1")
		require.NotNil(t, rule)
		require.Equal(t, "Rule One", rule.Name)
		require.Equal(t, "firing", rule.State)
	})

	t.Run("finds rule in second group", func(t *testing.T) {
		rule := findRuleInResponse(resp, "uid-3")
		require.NotNil(t, rule)
		require.Equal(t, "Rule Three", rule.Name)
		require.Equal(t, "pending", rule.State)
	})

	t.Run("returns nil for nonexistent UID", func(t *testing.T) {
		rule := findRuleInResponse(resp, "uid-missing")
		require.Nil(t, rule)
	})

	t.Run("returns nil for empty response", func(t *testing.T) {
		emptyResp := &rulesResponse{}
		rule := findRuleInResponse(emptyResp, "uid-1")
		require.Nil(t, rule)
	})

	t.Run("returns pointer to original rule (not copy)", func(t *testing.T) {
		rule := findRuleInResponse(resp, "uid-2")
		require.NotNil(t, rule)
		require.Equal(t, "Rule Two", rule.Name)
		// Verify it's a pointer into the original slice
		rule.Name = "Modified"
		require.Equal(t, "Modified", resp.Data.RuleGroups[0].Rules[1].Name)
		// Restore
		resp.Data.RuleGroups[0].Rules[1].Name = "Rule Two"
	})
}

func TestConvertAlertQueries(t *testing.T) {
	t.Run("auto-assigns RefID from index when empty", func(t *testing.T) {
		queries := []*AlertQuery{
			{DatasourceUID: "prometheus", Model: AlertQueryModel{Expr: "up"}},
			{DatasourceUID: "__expr__", Model: AlertQueryModel{Type: "math", Expression: "$A > 0"}},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.Len(t, result, 2)
		require.Equal(t, "A", result[0].RefID)
		require.Equal(t, "B", result[1].RefID)
	})

	t.Run("preserves explicit RefID", func(t *testing.T) {
		queries := []*AlertQuery{
			{RefID: "X", DatasourceUID: "prometheus", Model: AlertQueryModel{Expr: "up"}},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.Equal(t, "X", result[0].RefID)
	})

	t.Run("defaults RelativeTimeRange for non-expression queries", func(t *testing.T) {
		queries := []*AlertQuery{
			{DatasourceUID: "prometheus", Model: AlertQueryModel{Expr: "up"}},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.NotNil(t, result[0].RelativeTimeRange)
		require.Equal(t, models.Duration(600), result[0].RelativeTimeRange.From)
		require.Equal(t, models.Duration(0), result[0].RelativeTimeRange.To)
	})

	t.Run("does not default RelativeTimeRange for __expr__ queries", func(t *testing.T) {
		queries := []*AlertQuery{
			{DatasourceUID: "__expr__", Model: AlertQueryModel{Type: "math", Expression: "$A > 0"}},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.Nil(t, result[0].RelativeTimeRange)
	})

	t.Run("handles empty model", func(t *testing.T) {
		queries := []*AlertQuery{
			{DatasourceUID: "prometheus", Model: AlertQueryModel{}},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, "A", result[0].RefID)
	})

	t.Run("mixed data source and expression queries", func(t *testing.T) {
		queries := []*AlertQuery{
			{
				DatasourceUID:     "prometheus",
				RelativeTimeRange: &RelativeTimeRange{From: 300, To: 0},
				Model:             AlertQueryModel{Expr: "up{job=\"api\"}"},
			},
			{
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "reduce",
					Expression: "A",
					Reducer:    "last",
				},
			},
			{
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type:       "threshold",
					Expression: "B",
					Conditions: []AlertCondition{
						{Evaluator: ConditionEvaluator{Type: "gt", Params: []float64{0.95}}},
					},
				},
			},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.Len(t, result, 3)

		// Data source query
		require.Equal(t, "A", result[0].RefID)
		require.Equal(t, "prometheus", result[0].DatasourceUID)
		require.NotNil(t, result[0].RelativeTimeRange)
		require.Equal(t, models.Duration(300), result[0].RelativeTimeRange.From)

		// Reduce expression
		require.Equal(t, "B", result[1].RefID)
		require.Equal(t, "__expr__", result[1].DatasourceUID)
		require.Nil(t, result[1].RelativeTimeRange)

		// Threshold expression
		require.Equal(t, "C", result[2].RefID)
		require.Equal(t, "__expr__", result[2].DatasourceUID)
	})

	t.Run("empty input returns empty output", func(t *testing.T) {
		result, err := convertAlertQueries(nil)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("preserves explicit RelativeTimeRange", func(t *testing.T) {
		queries := []*AlertQuery{
			{
				DatasourceUID:     "prometheus",
				RelativeTimeRange: &RelativeTimeRange{From: 3600, To: 1800},
				Model:             AlertQueryModel{Expr: "up"},
			},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.NotNil(t, result[0].RelativeTimeRange)
		require.Equal(t, models.Duration(3600), result[0].RelativeTimeRange.From)
		require.Equal(t, models.Duration(1800), result[0].RelativeTimeRange.To)
	})

	t.Run("preserves extra model fields for Graphite queries", func(t *testing.T) {
		queries := []*AlertQuery{
			{
				RefID:         "C",
				DatasourceUID: "000000004",
				RelativeTimeRange: &RelativeTimeRange{From: 300, To: 0},
				Model: AlertQueryModel{
					Extra: map[string]any{
						"datasource": map[string]any{"type": "graphite", "uid": "000000004"},
						"target":     "alias(asPercent(sumSeries(a), sumSeries(b)), 'error rate')",
						"textEditor": true,
						"refId":      "C",
					},
				},
			},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, "C", result[0].RefID)

		model := result[0].Model.(map[string]any)
		require.Equal(t, "alias(asPercent(sumSeries(a), sumSeries(b)), 'error rate')", model["target"])
		require.Equal(t, true, model["textEditor"])
		ds, ok := model["datasource"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "graphite", ds["type"])
	})

	t.Run("preserves classic_conditions with operator, query, and reducer", func(t *testing.T) {
		queries := []*AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "__expr__",
				Model: AlertQueryModel{
					Type: "classic_conditions",
					Conditions: []AlertCondition{
						{
							Evaluator: ConditionEvaluator{Type: "gt", Params: []float64{0.1}},
							Operator:  &ConditionOperator{Type: "and"},
							Query:     &ConditionQuery{Params: []string{"C"}},
							Reducer:   &ConditionReducer{Type: "avg"},
						},
					},
				},
			},
		}
		result, err := convertAlertQueries(queries)
		require.NoError(t, err)
		require.Len(t, result, 1)

		model := result[0].Model.(map[string]any)
		conditions, ok := model["conditions"].([]any)
		require.True(t, ok)
		require.Len(t, conditions, 1)

		cond := conditions[0].(map[string]any)
		// Verify evaluator
		evaluator := cond["evaluator"].(map[string]any)
		require.Equal(t, "gt", evaluator["type"])
		// Verify operator is preserved
		operator := cond["operator"].(map[string]any)
		require.Equal(t, "and", operator["type"])
		// Verify query params (RefID reference) is preserved
		query := cond["query"].(map[string]any)
		params := query["params"].([]any)
		require.Equal(t, "C", params[0])
		// Verify reducer is preserved
		reducer := cond["reducer"].(map[string]any)
		require.Equal(t, "avg", reducer["type"])
	})
}

func TestAlertQueryModel_JSONRoundTrip(t *testing.T) {
	t.Run("round-trips Graphite model without data loss", func(t *testing.T) {
		input := `{
			"datasource": {"type": "graphite", "uid": "000000004"},
			"target": "alias(asPercent(a, b), 'rate')",
			"textEditor": true,
			"intervalMs": 1000,
			"maxDataPoints": 43200,
			"refCount": 0,
			"refId": "C"
		}`

		var model AlertQueryModel
		err := json.Unmarshal([]byte(input), &model)
		require.NoError(t, err)

		// Typed fields should be empty (Graphite doesn't use them)
		require.Empty(t, model.Expr)
		require.Empty(t, model.Type)

		// Extra should have all Graphite-specific fields
		require.Equal(t, "alias(asPercent(a, b), 'rate')", model.Extra["target"])
		require.Equal(t, true, model.Extra["textEditor"])
		require.Equal(t, float64(1000), model.Extra["intervalMs"])

		// Re-marshal and verify no data loss
		output, err := json.Marshal(model)
		require.NoError(t, err)

		var roundTripped map[string]any
		err = json.Unmarshal(output, &roundTripped)
		require.NoError(t, err)

		require.Equal(t, "alias(asPercent(a, b), 'rate')", roundTripped["target"])
		require.Equal(t, true, roundTripped["textEditor"])
		require.Equal(t, float64(1000), roundTripped["intervalMs"])
	})

	t.Run("round-trips classic_conditions with all fields", func(t *testing.T) {
		input := `{
			"type": "classic_conditions",
			"conditions": [
				{
					"evaluator": {"type": "gt", "params": [0.1]},
					"operator": {"type": "and"},
					"query": {"params": ["C"]},
					"reducer": {"type": "avg"}
				}
			]
		}`

		var model AlertQueryModel
		err := json.Unmarshal([]byte(input), &model)
		require.NoError(t, err)
		require.Equal(t, "classic_conditions", model.Type)
		require.Len(t, model.Conditions, 1)
		require.NotNil(t, model.Conditions[0].Operator)
		require.Equal(t, "and", model.Conditions[0].Operator.Type)
		require.NotNil(t, model.Conditions[0].Query)
		require.Equal(t, []string{"C"}, model.Conditions[0].Query.Params)
		require.NotNil(t, model.Conditions[0].Reducer)
		require.Equal(t, "avg", model.Conditions[0].Reducer.Type)

		// Re-marshal
		output, err := json.Marshal(model)
		require.NoError(t, err)

		var roundTripped map[string]any
		err = json.Unmarshal(output, &roundTripped)
		require.NoError(t, err)

		conditions := roundTripped["conditions"].([]any)
		cond := conditions[0].(map[string]any)
		query := cond["query"].(map[string]any)
		params := query["params"].([]any)
		require.Equal(t, "C", params[0])
	})
}

func TestExtractQuerySummaries(t *testing.T) {
	t.Run("extracts expr field (Prometheus)", func(t *testing.T) {
		data := []*models.AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "prometheus-uid",
				Model: map[string]any{
					"expr": `up{job="grafana"}`,
				},
			},
		}
		summaries := extractQuerySummaries(data)
		require.Len(t, summaries, 1)
		require.Equal(t, `up{job="grafana"}`, summaries[0].Expression)
	})

	t.Run("extracts expression field (Grafana expression)", func(t *testing.T) {
		data := []*models.AlertQuery{
			{
				RefID:         "B",
				DatasourceUID: "__expr__",
				Model: map[string]any{
					"expression": "A",
				},
			},
		}
		summaries := extractQuerySummaries(data)
		require.Len(t, summaries, 1)
		require.Equal(t, "A", summaries[0].Expression)
	})

	t.Run("extracts query field (Elasticsearch)", func(t *testing.T) {
		data := []*models.AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "elasticsearch-uid",
				Model: map[string]any{
					"query": `app:"random-service" AND error`,
				},
			},
		}
		summaries := extractQuerySummaries(data)
		require.Len(t, summaries, 1)
		require.Equal(t, `app:"random-service" AND error`, summaries[0].Expression)
	})

	t.Run("returns nil for empty data", func(t *testing.T) {
		summaries := extractQuerySummaries(nil)
		require.Nil(t, summaries)
	})

	t.Run("handles mixed datasource types", func(t *testing.T) {
		data := []*models.AlertQuery{
			{
				RefID:         "A",
				DatasourceUID: "elasticsearch-uid",
				Model: map[string]any{
					"query": `app:"random-service" AND log.level:"ERROR"`,
				},
			},
			{
				RefID:         "B",
				DatasourceUID: "__expr__",
				Model: map[string]any{
					"expression": "A",
				},
			},
			{
				RefID:         "C",
				DatasourceUID: "__expr__",
				Model: map[string]any{
					"expression": "B",
				},
			},
		}
		summaries := extractQuerySummaries(data)
		require.Len(t, summaries, 3)
		require.Equal(t, `app:"random-service" AND log.level:"ERROR"`, summaries[0].Expression)
		require.Equal(t, "A", summaries[1].Expression)
		require.Equal(t, "B", summaries[2].Expression)
	})
}
