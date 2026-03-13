package sql

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONMarshaling(t *testing.T) {
	frame := &JsonFrame{
		Name:    "test_frame",
		Columns: []string{"id", "name"},
		Rows: []map[string]any{
			{"id": 1, "name": "foo"},
			{"id": 2, "name": "bar"},
		},
		RowCount: 2,
	}

	result := &SQLQueryResult{
		JsonObject: &JsonObject{
			Status: 200,
			Frames: []*JsonFrame{frame},
		},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	// Unmarshal into a map to check keys
	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	// Check JsonObject keys (Status, Frames)
	assert.Contains(t, m, "status")
	assert.Contains(t, m, "frames")

	frames, ok := m["frames"].([]any)
	require.True(t, ok)
	require.Len(t, frames, 1)

	f, ok := frames[0].(map[string]any)
	require.True(t, ok)

	// Check JsonFrame keys (name, columns, rows, rowCount)
	assert.Contains(t, f, "name")
	assert.Contains(t, f, "columns")
	assert.Contains(t, f, "rows")
	assert.Contains(t, f, "rowCount")

	// Verify values
	assert.Equal(t, "test_frame", f["name"])
	assert.Equal(t, []any{"id", "name"}, f["columns"])

	rows, ok := f["rows"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 2)

	row1 := rows[0].(map[string]any)
	assert.Equal(t, float64(1), row1["id"]) // JSON numbers are floats
	assert.Equal(t, "foo", row1["name"])
}
