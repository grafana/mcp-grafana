//go:build unit

package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmptyResultGenericHintBudget(t *testing.T) {
	for _, tc := range []struct{ datasource, query, discovery string }{
		{"prometheus", "up", "list_prometheus_metric_names"},
		{"loki", `{job="app"}`, "list_loki_label_names"},
	} {
		t.Run(tc.datasource, func(t *testing.T) {
			hints := GenerateEmptyResultHints(HintContext{DatasourceType: tc.datasource, Query: tc.query})
			data, err := json.Marshal(hints)
			require.NoError(t, err)
			t.Logf("generic hint JSON (%d bytes): %s", len(data), data)
			// Generic advice is repeated for every empty query in a batch.
			// Keep it below 400 bytes, excluding optional debugging metadata.
			require.LessOrEqual(t, len(data), 400)
			require.Contains(t, string(data), tc.discovery)
		})
	}
}
