package tools

import (
	"encoding/json"
	"testing"

	"github.com/grafana/mcp-grafana/tools/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLQueryResponseMarshaling(t *testing.T) {
	frame := &sql.QueryResFrame{
		Name:    "test_frame",
		Columns: []string{"id", "val"},
		Rows: []map[string]any{
			{"id": 1, "val": "a"},
		},
		RowCount: 1,
	}

	result := &sql.SQLQueryResult{
		Status: 200,
		Frames: []*sql.QueryResFrame{frame},
	}

	response := &SQLQueryResponse{
		Result:         result,
		ProcessedQuery: "SELECT * FROM logs LIMIT 100",
		Hints: &EmptyResultHints{
			SuggestedActions: []string{"test action"},
		},
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	// Verify SQLQueryResult fields
	resMap := m["result"].(map[string]any)
	assert.Equal(t, float64(200), resMap["status"])
	assert.Contains(t, resMap, "frames")

	// Verify SQLQueryResponse specific fields
	assert.Equal(t, "SELECT * FROM logs LIMIT 100", m["processedQuery"])
	assert.Contains(t, m, "hints")

	// Verify frame structure
	frames := resMap["frames"].([]any)
	f := frames[0].(map[string]any)
	assert.Equal(t, []any{"id", "val"}, f["columns"])
	assert.Contains(t, f, "rows")

	rows := f["rows"].([]any)
	row1 := rows[0].(map[string]any)
	assert.Equal(t, float64(1), row1["id"])
	assert.Equal(t, "a", row1["val"])

	// Verify that row keys match columns
	columns := f["columns"].([]any)
	for _, col := range columns {
		assert.Contains(t, row1, col.(string))
	}
}

func TestListSQLTableSchemaResultMarshaling(t *testing.T) {
	result := &ListSQLTableSchemaResult{
		Schemas: map[string]*SQLSchemaResult{
			"logs": {
				Name: "logs",
				Fields: map[string]*SQLSchemaField{
					"id": {Type: "int"},
				},
			},
		},
		Errors: []string{"error1"},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Contains(t, m, "schemas")
	assert.Contains(t, m, "errors")

	schemas := m["schemas"].(map[string]any)
	assert.Contains(t, schemas, "logs")

	logs := schemas["logs"].(map[string]any)
	assert.Equal(t, "logs", logs["name"])

	fields := logs["fields"].(map[string]any)
	assert.Contains(t, fields, "id")

	id := fields["id"].(map[string]any)
	assert.Equal(t, "int", id["type"])
}
