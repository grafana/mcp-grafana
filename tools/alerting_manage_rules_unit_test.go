package tools

import (
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"
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
func TestManageRulesReadParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  ManageRulesReadParams
		wantErr string
	}{
		{
			name:   "list operation with defaults",
			params: ManageRulesReadParams{Operation: "list"},
		},
		{
			name:   "list operation with rule_limit",
			params: ManageRulesReadParams{Operation: "list", RuleLimit: 50},
		},
		{
			name:    "list operation with negative rule_limit",
			params:  ManageRulesReadParams{Operation: "list", RuleLimit: -1},
			wantErr: "invalid rule_limit",
		},
		{
			name:   "list operation with folder_uid",
			params: ManageRulesReadParams{Operation: "list", FolderUID: "folder-1"},
		},
		{
			name:   "list operation with search_folder",
			params: ManageRulesReadParams{Operation: "list", SearchFolder: "Production"},
		},
		{
			name:    "list operation with folder_uid and search_folder",
			params:  ManageRulesReadParams{Operation: "list", FolderUID: "folder-1", SearchFolder: "Production"},
			wantErr: "mutually exclusive",
		},
		{
			name:   "get operation with rule_uid",
			params: ManageRulesReadParams{Operation: "get", RuleUID: "test-uid"},
		},
		{
			name:    "get operation without rule_uid",
			params:  ManageRulesReadParams{Operation: "get"},
			wantErr: "rule_uid is required",
		},
		{
			name:    "unknown operation",
			params:  ManageRulesReadParams{Operation: "create"},
			wantErr: "unknown operation",
		},
		{
			name:    "empty operation",
			params:  ManageRulesReadParams{Operation: ""},
			wantErr: "unknown operation",
		},
		{
			name:    "delete not allowed in read params",
			params:  ManageRulesReadParams{Operation: "delete"},
			wantErr: "unknown operation",
		},
		{
			name:   "versions operation with rule_uid",
			params: ManageRulesReadParams{Operation: "versions", RuleUID: "test-uid"},
		},
		{
			name:    "versions operation without rule_uid",
			params:  ManageRulesReadParams{Operation: "versions"},
			wantErr: "rule_uid is required",
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

func TestManageRulesReadWriteParams_Validate(t *testing.T) {
	validCreateParams := ManageRulesReadWriteParams{
		Operation:    "create",
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

	validUpdateParams := ManageRulesReadWriteParams{
		Operation:    "update",
		RuleUID:      "test-uid",
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

	tests := []struct {
		name    string
		params  ManageRulesReadWriteParams
		wantErr string
	}{
		{
			name:   "list operation with defaults",
			params: ManageRulesReadWriteParams{Operation: "list"},
		},
		{
			name:   "list operation with rule_limit",
			params: ManageRulesReadWriteParams{Operation: "list", RuleLimit: 50},
		},
		{
			name:    "list operation with negative rule_limit",
			params:  ManageRulesReadWriteParams{Operation: "list", RuleLimit: -1},
			wantErr: "invalid rule_limit",
		},
		{
			name:   "list operation with search_folder",
			params: ManageRulesReadWriteParams{Operation: "list", SearchFolder: "Production"},
		},
		{
			name:    "list operation with folder_uid and search_folder",
			params:  ManageRulesReadWriteParams{Operation: "list", FolderUID: "folder-1", SearchFolder: "Production"},
			wantErr: "mutually exclusive",
		},
		{
			name:   "get operation with rule_uid",
			params: ManageRulesReadWriteParams{Operation: "get", RuleUID: "test-uid"},
		},
		{
			name:    "get operation without rule_uid",
			params:  ManageRulesReadWriteParams{Operation: "get"},
			wantErr: "rule_uid is required",
		},
		{
			name:   "create operation with valid params",
			params: validCreateParams,
		},
		{
			name: "create operation missing title",
			params: ManageRulesReadWriteParams{
				Operation:    "create",
				RuleGroup:    "test-group",
				FolderUID:    "test-folder",
				Condition:    "A",
				Data:         []*AlertQuery{{RefID: "A"}},
				NoDataState:  "OK",
				ExecErrState: "OK",
				For:          "5m",
				OrgID:        1,
			},
			wantErr: "title is required",
		},
		{
			name: "create operation missing rule_group",
			params: ManageRulesReadWriteParams{
				Operation:    "create",
				Title:        "Test Rule",
				FolderUID:    "test-folder",
				Condition:    "A",
				Data:         []*AlertQuery{{RefID: "A"}},
				NoDataState:  "OK",
				ExecErrState: "OK",
				For:          "5m",
				OrgID:        1,
			},
			wantErr: "rule_group is required",
		},
		{
			name:   "update operation with valid params",
			params: validUpdateParams,
		},
		{
			name: "update operation missing rule_uid",
			params: ManageRulesReadWriteParams{
				Operation:    "update",
				Title:        "Test Rule",
				RuleGroup:    "test-group",
				FolderUID:    "test-folder",
				Condition:    "A",
				Data:         []*AlertQuery{{RefID: "A"}},
				NoDataState:  "OK",
				ExecErrState: "OK",
				For:          "5m",
				OrgID:        1,
			},
			wantErr: "rule_uid is required",
		},
		{
			name:   "delete operation with rule_uid",
			params: ManageRulesReadWriteParams{Operation: "delete", RuleUID: "test-uid"},
		},
		{
			name:    "delete operation without rule_uid",
			params:  ManageRulesReadWriteParams{Operation: "delete"},
			wantErr: "rule_uid is required",
		},
		{
			name:   "versions operation with rule_uid",
			params: ManageRulesReadWriteParams{Operation: "versions", RuleUID: "test-uid"},
		},
		{
			name:    "versions operation without rule_uid",
			params:  ManageRulesReadWriteParams{Operation: "versions"},
			wantErr: "rule_uid is required",
		},
		{
			name:    "unknown operation",
			params:  ManageRulesReadWriteParams{Operation: "invalid"},
			wantErr: "unknown operation",
		},
		{
			name:    "empty operation",
			params:  ManageRulesReadWriteParams{Operation: ""},
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
}
