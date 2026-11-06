//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPrometheusMetricMetadataLimitPerMetric(t *testing.T) {
	const body = `{"status":"success","data":{
		"a_metric":[{"type":"counter","help":"first","unit":""},{"type":"counter","help":"second","unit":"seconds"}],
		"b_metric":[{"type":"gauge","help":"only","unit":""}]
	}}`

	for _, tc := range []struct {
		name           string
		limitPerMetric int
		expected       map[string]int
	}{
		{name: "unset returns every entry", expected: map[string]int{"a_metric": 2, "b_metric": 1}},
		{name: "one entry per metric", limitPerMetric: 1, expected: map[string]int{"a_metric": 1, "b_metric": 1}},
		{name: "limit above entry count", limitPerMetric: 5, expected: map[string]int{"a_metric": 2, "b_metric": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/datasources/uid/test":
					_ = json.NewEncoder(w).Encode(&models.DataSource{UID: "test", Type: "prometheus"})
				case "/api/datasources/uid/test/resources/api/v1/metadata":
					_, _ = w.Write([]byte(body))
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			metadata, err := listPrometheusMetricMetadata(mockDatasourcesCtx(server), ListPrometheusMetricMetadataParams{
				DatasourceUID:  "test",
				LimitPerMetric: tc.limitPerMetric,
			})
			require.NoError(t, err)

			counts := map[string]int{}
			for name, entries := range metadata {
				counts[name] = len(entries)
			}
			assert.Equal(t, tc.expected, counts)
			if tc.limitPerMetric == 1 {
				assert.Equal(t, "first", metadata["a_metric"][0].Help, "the first entry should be kept")
			}
		})
	}
}
