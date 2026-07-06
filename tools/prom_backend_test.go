//go:build unit

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestPrometheusBackend builds a prometheusBackend talking to the given test
// server, bypassing the Grafana datasource proxy plumbing.
func newTestPrometheusBackend(t *testing.T, server *httptest.Server) *prometheusBackend {
	t.Helper()
	c, err := api.NewClient(api.Config{Address: server.URL})
	require.NoError(t, err)
	return &prometheusBackend{api: promv1.NewAPI(c)}
}

func TestPrometheusBackendQuery_SurfacesWarnings(t *testing.T) {
	const body = `{
		"status": "success",
		"data": {"resultType": "vector", "result": []},
		"warnings": ["source cluster-b is unavailable, returning partial data"]
	}`

	for _, queryType := range []string{"instant", "range"} {
		t.Run(queryType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)

			b := newTestPrometheusBackend(t, server)
			end := time.Unix(1700000000, 0)
			_, warnings, err := b.Query(context.Background(), "up", queryType, end.Add(-time.Hour), end, 60)
			require.NoError(t, err)
			assert.Equal(t, promv1.Warnings{"source cluster-b is unavailable, returning partial data"}, warnings)
		})
	}
}

func TestPrometheusBackendQuery_NoWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(server.Close)

	b := newTestPrometheusBackend(t, server)
	_, warnings, err := b.Query(context.Background(), "up", "instant", time.Time{}, time.Unix(1700000000, 0), 0)
	require.NoError(t, err)
	assert.Empty(t, warnings)
}
