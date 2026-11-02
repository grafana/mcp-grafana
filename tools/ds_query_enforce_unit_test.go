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
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dsQueryGuardServer serves the datasource-lookup endpoint (mapping uid -> type)
// and records the last payload that reached /api/ds/query in *sent (nil means
// /api/ds/query was never reached).
// dsList is an optional /api/datasources list response (for default-datasource
// resolution); nil means the endpoint is not expected to be called.
func dsQueryGuardServer(t *testing.T, typeByUID map[string]string, dsList []map[string]interface{}, sent *map[string]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/ds/query":
			*sent = map[string]interface{}{}
			_ = json.NewDecoder(r.Body).Decode(sent)
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

// runDSQuery sends payload through doDSQuery against srv.
func runDSQuery(t *testing.T, srv *httptest.Server, enforce bool, payload map[string]interface{}) error {
	t.Helper()
	ctx := dsQueryGuardCtx(srv, enforce)
	client, base, err := newDSQueryHTTPClient(ctx)
	require.NoError(t, err)
	_, err = doDSQuery(ctx, client, base, payload)
	return err
}

// sentExprs returns the expr of each query in a payload received by
// dsQueryGuardServer, in order ("" for a query without one).
func sentExprs(t *testing.T, sent map[string]interface{}) []string {
	t.Helper()
	queries, ok := sent["queries"].([]interface{})
	require.True(t, ok, "sent payload has no query list")
	exprs := make([]string, 0, len(queries))
	for _, q := range queries {
		expr, _ := q.(map[string]interface{})["expr"].(string)
		exprs = append(exprs, expr)
	}
	return exprs
}

func lokiPayload(uid string) map[string]interface{} {
	return dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
		map[string]interface{}{
			"refId":      "A",
			"datasource": map[string]interface{}{"uid": uid, "type": "grafana-bigquery-datasource"},
			"expr":       `{app="secret"}`,
		})
}

const enforcedSecretExpr = `{app="secret", agent_safe="true"}`

// TestDoDSQuery_LokiEnforcement covers the /api/ds/query chokepoint: under
// enforcement every query that reaches a Loki datasource carries the enforced
// matchers, whatever type the payload declares, and anything that cannot be
// enforced is refused before any request is sent.
func TestDoDSQuery_LokiEnforcement(t *testing.T) {
	t.Run("loki expr rewritten with the enforced matchers", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &sent)
		require.NoError(t, runDSQuery(t, srv, true, lokiPayload("loki-uid")))
		assert.Equal(t, []string{enforcedSecretExpr}, sentExprs(t, sent))
	})

	t.Run("caller's payload is not modified, so a reused query is enforced once", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &sent)
		payload := lokiPayload("loki-uid")
		for range 2 {
			require.NoError(t, runDSQuery(t, srv, true, payload))
			assert.Equal(t, []string{enforcedSecretExpr}, sentExprs(t, sent))
		}
		assert.Equal(t, `{app="secret"}`, payload["queries"].([]map[string]interface{})[0]["expr"])
	})

	t.Run("only the Loki queries of a mixed payload are rewritten", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki", "prom-uid": "prometheus"}, nil, &sent)
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{"refId": "A", "datasource": map[string]interface{}{"uid": "prom-uid"}, "expr": `up{job="x"}`},
			map[string]interface{}{"refId": "B", "datasource": map[string]interface{}{"uid": "loki-uid"}, "expr": `{app="secret"}`},
		)
		require.NoError(t, runDSQuery(t, srv, true, payload))
		assert.Equal(t, []string{`up{job="x"}`, enforcedSecretExpr}, sentExprs(t, sent))
	})

	t.Run("loki query without an expr refused", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &sent)
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{"refId": "A", "datasource": map[string]interface{}{"uid": "loki-uid"}, "query": `{app="secret"}`})
		err := runDSQuery(t, srv, true, payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "without a LogQL expr")
		assert.Nil(t, sent)
	})

	t.Run("loki expr the matchers cannot be injected into refused", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &sent)
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{"refId": "A", "datasource": map[string]interface{}{"uid": "loki-uid"}, "expr": `vector(1)`})
		require.Error(t, runDSQuery(t, srv, true, payload))
		assert.Nil(t, sent)
	})

	t.Run("victorialogs datasource refused", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"vl-uid": victoriaLogsDatasourceType}, nil, &sent)
		err := runDSQuery(t, srv, true, lokiPayload("vl-uid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VictoriaLogs")
		assert.Nil(t, sent)
	})

	t.Run("non-loki datasource passes through unchanged under enforcement", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"pg-uid": "grafana-postgresql-datasource"}, nil, &sent)
		require.NoError(t, runDSQuery(t, srv, true, lokiPayload("pg-uid")))
		assert.Equal(t, []string{`{app="secret"}`}, sentExprs(t, sent))
	})

	t.Run("no enforcement: no lookup, payload sent unchanged", func(t *testing.T) {
		var sent map[string]interface{}
		// typeByUID intentionally empty: if the chokepoint tried to resolve the
		// type it would 404 and fail; with enforcement off it must not look up.
		srv := dsQueryGuardServer(t, map[string]string{}, nil, &sent)
		require.NoError(t, runDSQuery(t, srv, false, lokiPayload("loki-uid")))
		assert.Equal(t, []string{`{app="secret"}`}, sentExprs(t, sent))
	})

	t.Run("unverifiable datasource fails closed under enforcement", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{}, nil, &sent) // lookup 404s
		err := runDSQuery(t, srv, true, lokiPayload("mystery-uid"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not be verified")
		assert.Nil(t, sent)
	})

	t.Run("no-uid query refused when the org default is a log datasource", func(t *testing.T) {
		var sent map[string]interface{}
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": false},
			{"uid": "loki", "type": "loki", "isDefault": true},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &sent)
		err := runDSQuery(t, srv, true, noUIDPayload())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "default datasource")
		assert.Nil(t, sent)
	})

	t.Run("no-uid query allowed when the org default is not a log datasource", func(t *testing.T) {
		var sent map[string]interface{}
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": true},
			{"uid": "loki", "type": "loki", "isDefault": false},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &sent)
		require.NoError(t, runDSQuery(t, srv, true, noUIDPayload()))
		assert.NotNil(t, sent)
	})

	t.Run("magic \"default\" UID resolves against the org default (log default refused)", func(t *testing.T) {
		var sent map[string]interface{}
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": false},
			{"uid": "loki", "type": "loki", "isDefault": true},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &sent)
		err := runDSQuery(t, srv, true, defaultUIDPayload())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "default datasource")
		assert.Nil(t, sent)
	})

	t.Run("magic \"default\" UID allowed when the org default is not a log datasource", func(t *testing.T) {
		var sent map[string]interface{}
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": true},
			{"uid": "loki", "type": "loki", "isDefault": false},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &sent)
		require.NoError(t, runDSQuery(t, srv, true, defaultUIDPayload()))
		assert.NotNil(t, sent)
	})

	t.Run("literal \"default\"-UID log datasource refused even when the org default is not a log datasource", func(t *testing.T) {
		var sent map[string]interface{}
		// A real datasource whose UID is literally "default" is a log datasource,
		// while the org default is Prometheus. Grafana >= 13 routes uid "default"
		// to this literal datasource, so it must be refused.
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": true},
			{"uid": "default", "type": "loki", "isDefault": false},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &sent)
		require.Error(t, runDSQuery(t, srv, true, defaultUIDPayload()))
		assert.Nil(t, sent)
	})

	t.Run("magic \"default\": log ORG default refused even when a literal \"default\" DS is non-log", func(t *testing.T) {
		var sent map[string]interface{}
		// Inverse of the case above: the literal "default" DS is Prometheus but
		// the org default is Loki. On any Grafana that resolves "default" to the
		// org default, the query would reach Loki, so both candidates must be
		// checked and this refused.
		dsList := []map[string]interface{}{
			{"uid": "default", "type": "prometheus", "isDefault": false},
			{"uid": "lokids", "type": "loki", "isDefault": true},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &sent)
		require.Error(t, runDSQuery(t, srv, true, defaultUIDPayload()))
		assert.Nil(t, sent)
	})

	t.Run("a no-uid query does not stand in for a later \"default\"-uid query", func(t *testing.T) {
		var sent map[string]interface{}
		// The no-uid query only resolves to the org default (Prometheus); the
		// "default"-uid query can also reach the literal "default" datasource,
		// which is Loki, so it must be checked on its own and refused.
		dsList := []map[string]interface{}{
			{"uid": "mimir", "type": "prometheus", "isDefault": true},
			{"uid": "default", "type": "loki", "isDefault": false},
		}
		srv := dsQueryGuardServer(t, map[string]string{}, dsList, &sent)
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{"refId": "A", "expr": `up`},
			map[string]interface{}{"refId": "B", "datasource": map[string]interface{}{"uid": "default"}, "expr": `{app="secret"}`},
		)
		require.Error(t, runDSQuery(t, srv, true, payload))
		assert.Nil(t, sent)
	})

	t.Run("bare-string datasource resolves as a UID and is enforced for Loki", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &sent)
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{"refId": "A", "datasource": "loki-uid", "expr": `{app="secret"}`})
		require.NoError(t, runDSQuery(t, srv, true, payload))
		assert.Equal(t, []string{enforcedSecretExpr}, sentExprs(t, sent))
	})

	t.Run("unparseable datasource shape fails closed", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{}, nil, &sent)
		// datasource object with a non-string uid: present but not understood.
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{"refId": "A", "datasource": map[string]interface{}{"uid": 7}, "expr": `{app="secret"}`})
		require.Error(t, runDSQuery(t, srv, true, payload))
		assert.Nil(t, sent)
	})

	t.Run("legacy numeric datasourceId fails closed", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{}, nil, &sent)
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{"refId": "A", "datasourceId": 7, "expr": `{app="secret"}`})
		require.Error(t, runDSQuery(t, srv, true, payload))
		assert.Nil(t, sent)
	})

	t.Run("a valid datasource.uid wins over a leftover datasourceId", func(t *testing.T) {
		var sent map[string]interface{}
		// run_panel_query copies raw panel targets that keep datasourceId after
		// overwriting datasource; a real non-log uid must still resolve on the
		// uid, not be refused for the stale numeric id.
		srv := dsQueryGuardServer(t, map[string]string{"cw-uid": "cloudwatch"}, nil, &sent)
		payload := dsQueryPayload(time.Now().Add(-time.Hour), time.Now(),
			map[string]interface{}{
				"refId":        "A",
				"datasource":   map[string]interface{}{"uid": "cw-uid", "type": "cloudwatch"},
				"datasourceId": 7,
			})
		require.NoError(t, runDSQuery(t, srv, true, payload))
		assert.NotNil(t, sent)
	})

	t.Run("queries as []interface{} are still inspected (Loki enforced)", func(t *testing.T) {
		var sent map[string]interface{}
		srv := dsQueryGuardServer(t, map[string]string{"loki-uid": "loki"}, nil, &sent)
		// Envelope built with a []interface{} query list, as a JSON round-trip
		// would produce.
		payload := map[string]interface{}{
			"from": "now-1h", "to": "now",
			"queries": []interface{}{
				map[string]interface{}{"refId": "A", "datasource": map[string]interface{}{"uid": "loki-uid"}, "expr": `{app="secret"}`},
			},
		}
		require.NoError(t, runDSQuery(t, srv, true, payload))
		assert.Equal(t, []string{enforcedSecretExpr}, sentExprs(t, sent))
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

// TestQueryLokiLogs_EnforcedOnce checks the Loki tool's own path: its query
// reaches /api/ds/query with the enforced matchers injected exactly once.
func TestQueryLokiLogs_EnforcedOnce(t *testing.T) {
	server, payload := lokiQueryStub([2]string{"1000000001", "a1"})
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, true), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10,
		StartRFC3339: "1970-01-01T00:00:00Z", EndRFC3339: "1970-01-01T00:00:02Z",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`{app="a", agent_safe="true"}`}, sentExprs(t, *payload))
}

// TestBackendForDatasource_RejectsLogDatasources covers the Prometheus tools:
// they send the caller's expr to /api/ds/query, where a log datasource would
// run it as LogQL/LogsQL, so log datasources are rejected outright, with or
// without enforcement.
func TestBackendForDatasource_RejectsLogDatasources(t *testing.T) {
	for _, typ := range []string{"loki", victoriaLogsDatasourceType} {
		t.Run(typ, func(t *testing.T) {
			var sent map[string]interface{}
			srv := dsQueryGuardServer(t, map[string]string{"log-uid": typ}, nil, &sent)
			_, err := backendForDatasource(dsQueryGuardCtx(srv, false), "log-uid")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "log datasource")
			assert.Nil(t, sent)
		})
	}
}
