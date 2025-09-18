package tools

import (
	"testing"

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
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
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
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
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
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "ruleGroup is required")
	})

	t.Run("missing folder UID", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			Condition:    "A",
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "folderUID is required")
	})

	t.Run("missing condition", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
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
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "noDataState is required")
	})

	t.Run("missing exec error state", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:       "Test Rule",
			RuleGroup:   "test-group",
			FolderUID:   "test-folder",
			Condition:   "A",
			Data:        []interface{}{map[string]interface{}{"refId": "A"}},
			NoDataState: "OK",
			For:         "5m",
			OrgID:       1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "execErrState is required")
	})

	t.Run("missing for duration", func(t *testing.T) {
		params := CreateAlertRuleParams{
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
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
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        0,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "orgID is required and must be greater than 0")
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
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
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
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "uid is required")
	})

	t.Run("invalid org ID", func(t *testing.T) {
		params := UpdateAlertRuleParams{
			UID:          "test-uid",
			Title:        "Test Rule",
			RuleGroup:    "test-group",
			FolderUID:    "test-folder",
			Condition:    "A",
			Data:         []interface{}{map[string]interface{}{"refId": "A"}},
			NoDataState:  "OK",
			ExecErrState: "OK",
			For:          "5m",
			OrgID:        -1,
		}
		err := params.validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "orgID is required and must be greater than 0")
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
