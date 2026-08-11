//go:build unit

package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file drives real queries through the actual HTTP clients (Prometheus
// client_golang, the native Loki client) against a fake Grafana that mimics
// how each deployment generation answers the datasource proxy routes. These
// are behavior-level regression tests: asserting only on the path strings
// produced by the routing helpers previously missed that Grafana 8.x answers
// the uid-based proxy routes with a 400 — a status no transport retried — so
// every Prometheus query failed there (grafana/mcp-grafana#1015 review).
//
// Per-generation behavior of GET/POST /api/datasources/proxy/uid/{uid}/*,
// where the route does not exist and requests fall into the numeric :id
// route (verified against real instances):
//   - Grafana 7.5: 500 {"message":"Unable to load datasource meta data"}
//   - Grafana 8.5: 400 {"message":"id is invalid"}
// And on Grafana 13+ the numeric-id routes are disabled by default,
// answering 404.

const promScalarJSON = `{"status":"success","data":{"resultType":"scalar","result":[1700000000,"42"]}}`
const lokiLabelsJSON = `{"status":"success","data":["app","job"]}`

// legacyProfile describes how a Grafana deployment answers each route family.
type legacyProfile struct {
	// metadataOK serves the datasource metadata API instead of a 403.
	metadataOK bool
	// uidProxy is the response for /api/datasources/proxy/uid/{uid}/* when
	// non-zero; 0 means serve the datasource payload normally.
	uidProxyStatus int
	uidProxyBody   string
	// numericProxyStatus is the response for /api/datasources/proxy/{id}/*
	// when non-zero; 0 means serve the datasource payload normally.
	numericProxyStatus int
}

var legacyProfiles = map[string]legacyProfile{
	"grafana-7.5":     {uidProxyStatus: http.StatusInternalServerError, uidProxyBody: `{"message":"Unable to load datasource meta data"}`},
	"grafana-8.5":     {uidProxyStatus: http.StatusBadRequest, uidProxyBody: `{"message":"id is invalid"}`},
	"grafana-13-rbac": {numericProxyStatus: http.StatusNotFound},
	"modern":          {metadataOK: true},
}

// fakeGrafana records every request path in order and answers according to
// the profile.
type fakeGrafana struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
}

func (f *fakeGrafana) recordedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeGrafana) dataRequests() []string {
	var out []string
	for _, p := range f.recordedPaths() {
		if strings.HasPrefix(p, "/api/datasources/proxy/") || strings.Contains(p, "/resources/") {
			out = append(out, p)
		}
	}
	return out
}

func newFakeGrafana(profile legacyProfile) *fakeGrafana {
	f := &fakeGrafana{}
	serveDatasource := func(w http.ResponseWriter, path string) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(path, "/loki/api/v1/labels"):
			_, _ = w.Write([]byte(lokiLabelsJSON))
		default: // Prometheus /api/v1/query and friends
			_, _ = w.Write([]byte(promScalarJSON))
		}
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		f.mu.Lock()
		f.requests = append(f.requests, path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch {
		case path == "/api/frontend/settings":
			_, _ = w.Write([]byte(`{
				"datasources": {
					"-- Grafana --": {"id": -1, "uid": "grafana", "type": "datasource"},
					"Prometheus": {"id": 1, "uid": "prometheus", "type": "prometheus", "jsonData": {}},
					"Loki": {"id": 3, "uid": "loki", "type": "loki", "jsonData": {}}
				},
				"defaultDatasource": "Prometheus"
			}`))

		case strings.HasPrefix(path, "/api/datasources/proxy/uid/"):
			if profile.uidProxyStatus != 0 {
				w.WriteHeader(profile.uidProxyStatus)
				_, _ = w.Write([]byte(profile.uidProxyBody))
				return
			}
			serveDatasource(w, path)

		case strings.HasPrefix(path, "/api/datasources/uid/") && strings.Contains(path, "/resources/"):
			// The modern per-datasource resources route.
			serveDatasource(w, path)

		case strings.HasPrefix(path, "/api/datasources/proxy/"):
			if profile.numericProxyStatus != 0 {
				w.WriteHeader(profile.numericProxyStatus)
				_, _ = w.Write([]byte(`{"message":"Not found"}`))
				return
			}
			serveDatasource(w, path)

		case strings.HasPrefix(path, "/api/datasources/uid/"):
			// Datasource metadata API.
			if profile.metadataOK {
				uid := strings.TrimPrefix(path, "/api/datasources/uid/")
				if uid == "loki" {
					_, _ = w.Write([]byte(`{"id":3,"uid":"loki","name":"Loki","type":"loki","jsonData":{}}`))
				} else {
					_, _ = w.Write([]byte(`{"id":1,"uid":"prometheus","name":"Prometheus","type":"prometheus","jsonData":{}}`))
				}
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Permission denied"}`))

		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Permission denied"}`))
		}
	}))
	return f
}

// instantQuery runs a real instant query through backendForDatasource and the
// Prometheus client, returning the flattened result string.
func instantQuery(t *testing.T, f *fakeGrafana) string {
	t.Helper()
	// The transport's fallback cache is keyed by path (not host), so isolate
	// each test from paths cached by earlier ones.
	resetFallbackCache()
	ctx := mockDatasourcesCtx(f.Server)
	result, err := queryPrometheus(ctx, QueryPrometheusParams{
		DatasourceUID: "prometheus",
		Expr:          "time()",
		QueryType:     "instant",
		EndTime:       "now",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	return result.String()
}

// On deployments whose metadata API is inaccessible (Grafana before 9.0), a
// real Prometheus query must succeed by routing through the numeric-id proxy
// path directly — regardless of which status the deployment answers on the
// nonexistent uid-based routes (500 on 7.5, 400 on 8.5).
func TestQueryPrometheus_LegacyDeployments(t *testing.T) {
	for _, name := range []string{"grafana-7.5", "grafana-8.5"} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGrafana(legacyProfiles[name])
			defer f.Close()

			assert.Contains(t, instantQuery(t, f), "42")

			data := f.dataRequests()
			require.NotEmpty(t, data)
			assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"),
				"first data request must use the numeric-id proxy route, got %q", data[0])
		})
	}
}

// On a newer Grafana where an RBAC-restricted token cannot read datasource
// metadata but the numeric-id routes are disabled (404, default since 13.0),
// the transport must fall back to the uid-based proxy route.
func TestQueryPrometheus_NumericRoutesDisabled(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-13-rbac"])
	defer f.Close()

	assert.Contains(t, instantQuery(t, f), "42")

	data := f.dataRequests()
	require.GreaterOrEqual(t, len(data), 2)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"), "got %q", data[0])
	assert.True(t, strings.HasPrefix(data[1], "/api/datasources/proxy/uid/prometheus/"), "got %q", data[1])
}

// When the metadata API is accessible, behavior must be identical to upstream:
// uid-based routes only, and the frontend-settings fallback never engages.
func TestQueryPrometheus_ModernPathUnchanged(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["modern"])
	defer f.Close()

	assert.Contains(t, instantQuery(t, f), "42")

	for _, p := range f.recordedPaths() {
		assert.NotEqual(t, "/api/frontend/settings", p, "modern path must not consult frontend settings")
		assert.False(t, strings.HasPrefix(p, "/api/datasources/proxy/1/"),
			"modern path must not use numeric-id routes, got %q", p)
	}
}

// Callers may reference a datasource by name (fallbackDatasourceByUID
// resolves names as a convenience). The uid-based transport fallback must
// then be built from the datasource's *resolved* uid, not the caller's
// string: on a deployment with the numeric routes disabled,
// /api/datasources/proxy/uid/{name} would miss.
func TestQueryPrometheus_NameReferenceUsesResolvedUID(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-13-rbac"])
	defer f.Close()

	resetFallbackCache()
	ctx := mockDatasourcesCtx(f.Server)
	result, err := queryPrometheus(ctx, QueryPrometheusParams{
		DatasourceUID: "Prometheus", // the *name*; the uid is "prometheus"
		Expr:          "time()",
		QueryType:     "instant",
		EndTime:       "now",
	})
	require.NoError(t, err)
	assert.Contains(t, result.String(), "42")

	data := f.dataRequests()
	require.GreaterOrEqual(t, len(data), 2)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"), "got %q", data[0])
	assert.True(t, strings.HasPrefix(data[1], "/api/datasources/proxy/uid/prometheus/"),
		"transport fallback must use the resolved uid, got %q", data[1])
}

// Name references are matched case-insensitively, and the routing cache must
// agree with that: a query referencing the datasource by a differently-cased
// name has to reach the numeric-id route end to end.
func TestQueryPrometheus_CaseInsensitiveNameReference(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-8.5"])
	defer f.Close()

	resetFallbackCache()
	ctx := mockDatasourcesCtx(f.Server)
	result, err := queryPrometheus(ctx, QueryPrometheusParams{
		DatasourceUID: "PROMETHEUS", // the name is "Prometheus", the uid "prometheus"
		Expr:          "time()",
		QueryType:     "instant",
		EndTime:       "now",
	})
	require.NoError(t, err)
	assert.Contains(t, result.String(), "42")

	data := f.dataRequests()
	require.NotEmpty(t, data)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"), "got %q", data[0])
}

// The native Loki client shares the proxy routing; it must work on Grafana
// 8.x the same way.
func TestLokiClient_LegacyGrafana85(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-8.5"])
	defer f.Close()

	resetFallbackCache()
	ctx := mockDatasourcesCtx(f.Server)
	labels, err := listLokiLabelNames(ctx, ListLokiLabelNamesParams{DatasourceUID: "loki"})
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "job"}, labels)

	data := f.dataRequests()
	require.NotEmpty(t, data)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/3/"),
		"first data request must use the numeric-id proxy route, got %q", data[0])
}
