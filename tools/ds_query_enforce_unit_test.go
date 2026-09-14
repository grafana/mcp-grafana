//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	openapiclient "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dsQueryGuardServer serves the datasource-lookup endpoint (mapping uid -> type)
// and records whether /api/ds/query was ever reached.
// dsList is an optional /api/datasources list response (for default-datasource
// resolution); nil means the endpoint is not expected to be called.
func dsQueryGuardServer(t *testing.T, typeByUID map[string]string, dsList []map[string]interface{}, dsQueryHit *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/ds/query":
			*dsQueryHit = true
			resp := backend.QueryDataResponse{Responses: backend.Responses{
				"A": backend.DataResponse{Frames: data.Frames{data.NewFrame("",
					data.NewField("v", nil, []int64{1}))}},
			}}
			b, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		case len(r.URL.Path) > len("/api/datasources/uid/") && r.URL.Path[:len("/api/datasources/uid/")] == "/api/datasources/uid/":
			uid := r.URL.Path[len("/api/datasources/uid/"):]
			typ, ok := typeByUID[uid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"uid": uid, "type": typ, "name": uid})
		case r.URL.Path == "/api/datasources":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dsList)
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dsQueryGuardCtx(srv *httptest.Server, enforce bool) context.Context {
	u, _ := url.Parse(srv.URL)
	cfg := openapiclient.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"
	c := openapiclient.NewHTTPClientWithConfig(nil, cfg)
	ctx := mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
	gc := mcpgrafana.GrafanaConfig{URL: srv.URL}
	if enforce {
		m, err := ParseEnforcedMatchers(`agent_safe="true"`)
		if err != nil {
			panic(err)
		}
		gc.LokiEnforcedMatchers = m
	}
	return mcpgrafana.WithGrafanaConfig(ctx, gc)
}

func lokiPayload(uid string) map[string]interface{} {
	return dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
		map[string]interface{}{
			"refId":      "A",
			"datasource": map[string]interface{}{"uid": uid, "type": "grafana-bigquery-datasource"},
			"expr":       `{app="secret"}`,
		})
}

// TestDoDSQuery_LokiEnforcementGuard covers the /api/ds/query chokepoint: under
// enforcement a Loki datasource must be refused before any request is sent,
// regardless of the type declared in the payload.
func TestDoDSQuery_LokiEnforcementGuard(t *testing.T) {
	t.Run("loki datasource refused, ds/query never sent", func(t *testing.T) {
		var hit bool
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &hit)
		client, base, err := newDSQueryHTTPClient(dsQueryGuardCtx(srv, true))
		require.NoError(t, err)
		_, err = doDSQuery(dsQueryGuardCtx(srv, true), client, base, lokiPayload("loki-uid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "log datasource")
		assert.False(t, hit, "/api/ds/query must not be reached for a Loki datasource under enforcement")
	})

	t.Run("victorialogs datasource refused", func(t *testing.T) {
		var hit bool
		srv := dsQueryGuardServer(t, map[string]string{"vl-uid": victoriaLogsDatasourceType}, nil, &hit)
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, lokiPayload("vl-uid"))
		require.Error(t, err)
		assert.False(t, hit)
	})

	t.Run("non-loki datasource passes through under enforcement", func(t *testing.T) {
		var hit bool
		srv := dsQueryGuardServer(t, map[string]string{"pg-uid": "grafana-postgresql-datasource"}, nil, &hit)
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, lokiPayload("pg-uid"))
		require.NoError(t, err)
		assert.True(t, hit, "a non-log datasource must still reach /api/ds/query")
	})

	t.Run("no enforcement: guard is a no-op, no lookup, ds/query sent", func(t *testing.T) {
		var hit bool
		// typeByUID intentionally empty: if the guard tried to resolve the type
		// it would 404 and fail; with enforcement off it must not look up at all.
		srv := dsQueryGuardServer(t, map[string]string{}, nil, &hit)
		ctx := dsQueryGuardCtx(srv, false)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, lokiPayload("loki-uid"))
		require.NoError(t, err)
		assert.True(t, hit)
	})

	t.Run("unverifiable datasource fails closed under enforcement", func(t *testing.T) {
		var hit bool
		srv := dsQueryGuardServer(t, map[string]string{}, nil, &hit) // lookup 404s
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, lokiPayload("mystery-uid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not be verified")
		assert.False(t, hit)
	})

	t.Run("no-uid query refused when the org default is a log datasource", func(t *testing.T) {
		var hit bool
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": false},
			{"uid": "loki", "type": "loki", "isDefault": true},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &hit)
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, noUIDPayload())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "default log datasource")
		assert.False(t, hit)
	})

	t.Run("no-uid query allowed when the org default is not a log datasource", func(t *testing.T) {
		var hit bool
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": true},
			{"uid": "loki", "type": "loki", "isDefault": false},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &hit)
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, noUIDPayload())
		require.NoError(t, err)
		assert.True(t, hit)
	})

	t.Run("magic \"default\" UID resolves against the org default (log default refused)", func(t *testing.T) {
		var hit bool
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": false},
			{"uid": "loki", "type": "loki", "isDefault": true},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &hit)
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, defaultUIDPayload())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "default log datasource")
		assert.False(t, hit)
	})

	t.Run("magic \"default\" UID allowed when the org default is not a log datasource", func(t *testing.T) {
		var hit bool
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": true},
			{"uid": "loki", "type": "loki", "isDefault": false},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &hit)
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, defaultUIDPayload())
		require.NoError(t, err)
		assert.True(t, hit)
	})

	t.Run("literal \"default\"-UID log datasource refused even when the org default is not a log datasource", func(t *testing.T) {
		var hit bool
		// A real datasource whose UID is literally "default" is a log datasource,
		// while the org default is Prometheus. Grafana >= 13 routes uid "default"
		// to this literal datasource, so it must be refused (Bugbot #2).
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": true},
			{"uid": "default", "type": "loki", "isDefault": false},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &hit)
		ctx := dsQueryGuardCtx(srv, true)
		client, base, err := newDSQueryHTTPClient(ctx)
		require.NoError(t, err)
		_, err = doDSQuery(ctx, client, base, defaultUIDPayload())
		require.Error(t, err)
		assert.False(t, hit)
	})
}

// noUIDPayload is an /api/ds/query payload whose query names no datasource,
// resolving against the org default.
func noUIDPayload() map[string]interface{} {
	return dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
		map[string]interface{}{"refId": "A", "expr": `{app="secret"}`})
}

// defaultUIDPayload is an /api/ds/query payload that references the org default
// datasource by Grafana's magic "default" UID.
func defaultUIDPayload() map[string]interface{} {
	return dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
		map[string]interface{}{
			"refId":      "A",
			"datasource": map[string]interface{}{"uid": "default"},
			"expr":       `{app="secret"}`,
		})
}

// TestBackendForDatasource_RejectsLoki covers the Prometheus-client route: a
// Loki datasource must be rejected outright rather than sent to the Prometheus
// proxy endpoint (which Loki does not serve).
func TestBackendForDatasource_RejectsLoki(t *testing.T) {
	var hit bool
	srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &hit)
	ctx := dsQueryGuardCtx(srv, false) // rejection is unconditional, no enforcement needed
	_, err := backendForDatasource(ctx, "loki-uid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log datasource")
}
