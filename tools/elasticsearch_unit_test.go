package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
