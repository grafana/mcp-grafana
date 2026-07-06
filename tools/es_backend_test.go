package tools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeFieldFromDataSource(t *testing.T) {
	t.Run("configured custom field", func(t *testing.T) {
		ds := &models.DataSource{
			JSONData: map[string]interface{}{
				"timeField": "timestamp",
			},
		}
		assert.Equal(t, "timestamp", timeFieldFromDataSource(ds))
	})

	t.Run("empty timeField returns default", func(t *testing.T) {
		ds := &models.DataSource{
			JSONData: map[string]interface{}{
				"timeField": "",
			},
		}
		assert.Equal(t, defaultTimeField, timeFieldFromDataSource(ds))
	})

	t.Run("missing timeField returns default", func(t *testing.T) {
		ds := &models.DataSource{
			JSONData: map[string]interface{}{
				"index": "logs-*",
			},
		}
		assert.Equal(t, defaultTimeField, timeFieldFromDataSource(ds))
	})

	t.Run("nil JSONData returns default", func(t *testing.T) {
		ds := &models.DataSource{}
		assert.Equal(t, defaultTimeField, timeFieldFromDataSource(ds))
	})

	t.Run("nil datasource returns default", func(t *testing.T) {
		assert.Equal(t, defaultTimeField, timeFieldFromDataSource(nil))
	})
}

func TestBuildElasticsearchQueryTimeField(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	t.Run("custom field in sort and range", func(t *testing.T) {
		query := esSearchQuery{query: "*", startTime: start, endTime: end, size: 10, timeField: "timestamp"}.build()

		sort, ok := query["sort"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, sort, 1)
		assert.Contains(t, sort[0], "timestamp")
		assert.NotContains(t, sort[0], defaultTimeField)

		queryClause, ok := query["query"].(map[string]interface{})
		require.True(t, ok)
		boolClause, ok := queryClause["bool"].(map[string]interface{})
		require.True(t, ok)
		must, ok := boolClause["must"].([]map[string]interface{})
		require.True(t, ok)
		require.NotEmpty(t, must)

		rangeClause, ok := must[0]["range"].(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, rangeClause, "timestamp")
		tsRange, ok := rangeClause["timestamp"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, start.Format(time.RFC3339), tsRange["gte"])
		assert.Equal(t, end.Format(time.RFC3339), tsRange["lte"])
	})

	t.Run("default field unchanged", func(t *testing.T) {
		query := esSearchQuery{startTime: start, endTime: end, size: 5, timeField: defaultTimeField}.build()

		sort, ok := query["sort"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, sort, 1)
		assert.Contains(t, sort[0], defaultTimeField)

		queryBytes, err := json.Marshal(query)
		require.NoError(t, err)
		assert.Contains(t, string(queryBytes), `"@timestamp"`)
	})
}

func TestFramesToDocumentsTimeField(t *testing.T) {
	doc := json.RawMessage(`{
		"_index": "logs-2024",
		"_id": "1",
		"timestamp": ["2024-06-15T12:00:00Z"],
		"message": "hello"
	}`)
	frames := data.Frames{
		data.NewFrame("A",
			data.NewField("A", nil, []json.RawMessage{doc}),
		),
	}

	docs, err := framesToDocuments(frames, "timestamp")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "2024-06-15T12:00:00Z", docs[0].Timestamp)
	assert.Equal(t, "hello", docs[0].Source["message"])
}
