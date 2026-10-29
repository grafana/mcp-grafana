//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file drives real requests through the actual HTTP clients (Prometheus
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

const promLabelsJSON = `{"status":"success","data":["instance","job"]}`
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
	numericProxyBody   string
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
		default: // Prometheus /api/v1/labels and friends
			_, _ = w.Write([]byte(promLabelsJSON))
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

		case path == "/api/ds/query":
			// Prometheus queries, on every deployment.
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{
				"A": backend.DataResponse{Frames: data.Frames{data.NewFrame("",
					data.NewField("Time", nil, []time.Time{time.Unix(1700000000, 0)}),
					data.NewField("Value", nil, []float64{42}),
				)}},
			}})

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
				body := profile.numericProxyBody
				if body == "" {
					body = `{"message":"Not found"}`
				}
				_, _ = w.Write([]byte(body))
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

// instantQuery runs a real instant query through backendForDatasource,
// returning the flattened result string.
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

// labelNames lists Prometheus label names through backendForDatasource and
// the Prometheus client, which use the datasource proxy routes.
func labelNames(t *testing.T, f *fakeGrafana, uid string) []string {
	t.Helper()
	// The transport's fallback cache is keyed by path (not host), so isolate
	// each test from paths cached by earlier ones.
	resetFallbackCache()
	names, err := listPrometheusLabelNames(mockDatasourcesCtx(f.Server), ListPrometheusLabelNamesParams{DatasourceUID: uid})
	require.NoError(t, err)
	return names
}

var wantLabels = []string{"instance", "job"}

// On deployments whose metadata API is inaccessible (Grafana before 9.0),
// Prometheus discovery calls must succeed by routing through the numeric-id
// proxy path directly — regardless of which status the deployment answers on
// the nonexistent uid-based routes (500 on 7.5, 400 on 8.5).
func TestListPrometheusLabelNames_LegacyDeployments(t *testing.T) {
	for _, name := range []string{"grafana-7.5", "grafana-8.5"} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGrafana(legacyProfiles[name])
			defer f.Close()

			assert.Equal(t, wantLabels, labelNames(t, f, "prometheus"))

			data := f.dataRequests()
			require.NotEmpty(t, data)
			assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"),
				"first data request must use the numeric-id proxy route, got %q", data[0])
		})
	}
}

// Queries always go through /api/ds/query, even when the datasource was
// resolved through the frontend-settings fallback.
func TestQueryPrometheus_FallbackDatasourceUsesDSQuery(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-8.5"])
	defer f.Close()

	assert.Contains(t, instantQuery(t, f), "42")
	assert.Contains(t, f.recordedPaths(), "/api/ds/query")
	assert.Empty(t, f.dataRequests(), "queries must not use the proxy routes")
}

// On a newer Grafana where an RBAC-restricted token cannot read datasource
// metadata but the numeric-id routes are disabled (404, default since 13.0),
// the transport must fall back to the uid-based proxy route.
func TestListPrometheusLabelNames_NumericRoutesDisabled(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-13-rbac"])
	defer f.Close()

	assert.Equal(t, wantLabels, labelNames(t, f, "prometheus"))

	data := f.dataRequests()
	require.GreaterOrEqual(t, len(data), 2)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"), "got %q", data[0])
	assert.True(t, strings.HasPrefix(data[1], "/api/datasources/proxy/uid/prometheus/"), "got %q", data[1])
}

// When the metadata API is accessible, queries go through /api/ds/query and
// the frontend-settings fallback never engages.
func TestQueryPrometheus_ModernPathUnchanged(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["modern"])
	defer f.Close()

	assert.Contains(t, instantQuery(t, f), "42")
	assert.Contains(t, f.recordedPaths(), "/api/ds/query", "modern path must query through /api/ds/query")
	assert.Equal(t, wantLabels, labelNames(t, f, "prometheus"))

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
func TestListPrometheusLabelNames_NameReferenceUsesResolvedUID(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-13-rbac"])
	defer f.Close()

	// The *name*; the uid is "prometheus".
	assert.Equal(t, wantLabels, labelNames(t, f, "Prometheus"))

	data := f.dataRequests()
	require.GreaterOrEqual(t, len(data), 2)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"), "got %q", data[0])
	assert.True(t, strings.HasPrefix(data[1], "/api/datasources/proxy/uid/prometheus/"),
		"transport fallback must use the resolved uid, got %q", data[1])
}

// Name references are matched case-insensitively, and the routing cache must
// agree with that: a call referencing the datasource by a differently-cased
// name has to reach the numeric-id route end to end.
func TestListPrometheusLabelNames_CaseInsensitiveNameReference(t *testing.T) {
	f := newFakeGrafana(legacyProfiles["grafana-8.5"])
	defer f.Close()

	// The name is "Prometheus", the uid "prometheus".
	assert.Equal(t, wantLabels, labelNames(t, f, "PROMETHEUS"))

	data := f.dataRequests()
	require.NotEmpty(t, data)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"), "got %q", data[0])
}

// A genuine error from the working numeric primary (here: the datasource
// timing out with a 500) must reach the caller as-is. Retrying it against
// the uid-based route — which answers 400 "id is invalid" on Grafana 8.x —
// would mask the real error with a routing error, so legacy mode retries on
// 404 only.
func TestListPrometheusLabelNames_LegacyRealErrorsNotMasked(t *testing.T) {
	f := newFakeGrafana(legacyProfile{
		uidProxyStatus:     http.StatusBadRequest,
		uidProxyBody:       `{"message":"id is invalid"}`,
		numericProxyStatus: http.StatusInternalServerError,
		numericProxyBody:   `{"status":"error","errorType":"timeout","error":"query timed out"}`,
	})
	defer f.Close()

	resetFallbackCache()
	ctx := mockDatasourcesCtx(f.Server)
	_, err := listPrometheusLabelNames(ctx, ListPrometheusLabelNamesParams{DatasourceUID: "prometheus"})
	require.Error(t, err)
	// The client surfaces the primary's real status (the response body lands
	// in the error detail); the essential property is that the 500 from the
	// numeric primary is what reaches the caller...
	assert.Contains(t, err.Error(), "server error: 500")
	// ...and not the uid route's routing garbage.
	assert.NotContains(t, err.Error(), "id is invalid")

	// The uid-based route must not have been consulted at all.
	for _, p := range f.dataRequests() {
		assert.False(t, strings.HasPrefix(p, "/api/datasources/proxy/uid/"),
			"real primary error must not trigger a uid-route retry, got %q", p)
	}
}

// The shared transport cache is keyed by bare path strings, and numeric-id
// paths like /api/datasources/proxy/1/... are identical across tenants. A
// newer-Grafana tenant whose numeric routes are disabled must not pin the
// uid-based path for a Grafana 8.x tenant sharing the same datasource id —
// legacy-mode transports therefore bypass the shared cache entirely.
func TestListPrometheusLabelNames_NoCrossTenantCachePoisoning(t *testing.T) {
	resetFallbackCache()

	// Tenant A: numeric routes disabled; the transport retry lands on the
	// uid-based route. Before the fix this recorded a fallback cache hit for
	// the bare path "/api/datasources/proxy/1/api/v1/labels".
	fA := newFakeGrafana(legacyProfiles["grafana-13-rbac"])
	defer fA.Close()
	namesA, err := listPrometheusLabelNames(mockDatasourcesCtx(fA.Server), ListPrometheusLabelNamesParams{DatasourceUID: "prometheus"})
	require.NoError(t, err)
	assert.Equal(t, wantLabels, namesA)

	// Tenant B: Grafana 8.5 with the same datasource id. Its call must still
	// go through the numeric primary — a poisoned cache would rewrite it to
	// the uid route, which answers 400 there.
	fB := newFakeGrafana(legacyProfiles["grafana-8.5"])
	defer fB.Close()
	namesB, err := listPrometheusLabelNames(mockDatasourcesCtx(fB.Server), ListPrometheusLabelNamesParams{DatasourceUID: "prometheus"})
	require.NoError(t, err)
	assert.Equal(t, wantLabels, namesB)

	data := fB.dataRequests()
	require.NotEmpty(t, data)
	assert.True(t, strings.HasPrefix(data[0], "/api/datasources/proxy/1/"),
		"tenant B must not inherit tenant A's cached fallback, got %q", data[0])
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
