//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPrometheusLabelDiscoveryRejectsNegativeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/datasources/uid/prom" {
			_ = json.NewEncoder(w).Encode(&models.DataSource{UID: "prom", Type: "prometheus"})
			return
		}
		if strings.Contains(r.URL.Path, "/api/v1/label") {
			_, _ = w.Write([]byte(`{"status":"success","data":["a","b","c"]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	t.Run("label names", func(t *testing.T) {
		names, err := listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{DatasourceUID: "prom", Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, names)

		assert.NotPanics(t, func() {
			_, err = listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{DatasourceUID: "prom", Limit: -1})
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
	})

	t.Run("label values", func(t *testing.T) {
		values, err := listPrometheusLabelValues(ctx, ListPrometheusLabelValuesParams{DatasourceUID: "prom", LabelName: "job", Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, values)

		assert.NotPanics(t, func() {
			_, err = listPrometheusLabelValues(ctx, ListPrometheusLabelValuesParams{DatasourceUID: "prom", LabelName: "job", Limit: -1})
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
	})
}
