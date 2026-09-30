//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
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

func TestBackendForDatasource_RejectsTempoDatasource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/datasources/uid/tempo-uid", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(&models.DataSource{
			UID:  "tempo-uid",
			Type: "tempo",
		})
	}))
	t.Cleanup(server.Close)

	ctx := mockDatasourcesCtx(server)

	backend, err := backendForDatasource(ctx, "tempo-uid")
	require.Error(t, err)
	assert.Nil(t, backend)
	assert.Contains(t, err.Error(), `datasource tempo-uid is of type "tempo", which is not a supported Prometheus-compatible datasource`)
}

func TestBackendForDatasource_AcceptsPrometheusDatasource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/datasources/uid/prom-uid", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(&models.DataSource{
			UID:  "prom-uid",
			Type: "prometheus",
		})
	}))
	t.Cleanup(server.Close)

	ctx := mockDatasourcesCtx(server)

	backend, err := backendForDatasource(ctx, "prom-uid")
	require.NoError(t, err)
	assert.NotNil(t, backend)
}

func TestPrometheusBackendQuery_IncludesErrorBody(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, want string
		status                        int
	}{
		{
			name:        "prometheus json",
			status:      http.StatusServiceUnavailable,
			contentType: "application/json",
			body:        `{"status":"error","errorType":"timeout","error":"query timed out in expression evaluation"}`,
			want:        "server error: 503: timeout: query timed out in expression evaluation",
		},
		{
			name:        "html",
			status:      http.StatusBadGateway,
			contentType: "text/html",
			body:        "<html><body>502 Bad Gateway</body></html>\n",
			want:        "server error: 502: <html><body>502 Bad Gateway</body></html>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			_, _, err := newTestPrometheusBackend(t, server).Query(context.Background(), "up", "instant", time.Now(), time.Now(), 0)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			var apiErr *promv1.Error
			assert.True(t, errors.As(err, &apiErr), "original *promv1.Error should still be unwrappable")
		})
	}
}
