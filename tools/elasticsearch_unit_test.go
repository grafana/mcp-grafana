package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsElasticsearchCompatibleType(t *testing.T) {
	tests := []struct {
		name     string
		dsType   string
		expected bool
	}{
		{"elasticsearch type", "elasticsearch", true},
		{"opensearch type", "opensearch", true},
		{"grafana-opensearch-datasource type", "grafana-opensearch-datasource", true},
		{"prometheus type", "prometheus", false},
		{"loki type", "loki", false},
		{"empty type", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isElasticsearchCompatibleType(tt.dsType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHitsTotalUnmarshalJSON(t *testing.T) {
	t.Run("number format (OpenSearch/ES6)", func(t *testing.T) {
		data := []byte(`42`)
		var ht HitsTotal
		err := json.Unmarshal(data, &ht)
		require.NoError(t, err)
		assert.Equal(t, 42, ht.Value)
		assert.Equal(t, "eq", ht.Relation)
	})

	t.Run("object format (ES7+)", func(t *testing.T) {
		data := []byte(`{"value":100,"relation":"gte"}`)
		var ht HitsTotal
		err := json.Unmarshal(data, &ht)
		require.NoError(t, err)
		assert.Equal(t, 100, ht.Value)
		assert.Equal(t, "gte", ht.Relation)
	})

	t.Run("zero number", func(t *testing.T) {
		data := []byte(`0`)
		var ht HitsTotal
		err := json.Unmarshal(data, &ht)
		require.NoError(t, err)
		assert.Equal(t, 0, ht.Value)
		assert.Equal(t, "eq", ht.Relation)
	})
}
