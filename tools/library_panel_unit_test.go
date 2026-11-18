//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// libraryElementJSON is the response of GET /api/library-elements/{uid}.
func libraryElementJSON(uid string, model map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"result": map[string]interface{}{
		"uid": uid, "name": uid, "kind": 1, "type": "timeseries", "model": model,
	}}
}

func libraryPanelRef(id int, title, uid string) map[string]interface{} {
	return map[string]interface{}{
		"id": float64(id), "title": title,
		"gridPos":      map[string]interface{}{"h": 8, "w": 12, "x": 0, "y": 0},
		"libraryPanel": map[string]interface{}{"uid": uid, "name": uid},
	}
}

// libraryServer serves a dashboard and library elements, and counts how many
// times each library element is fetched. A uid missing from elements is a 404.
func libraryServer(t *testing.T, dashboard map[string]interface{}, elements map[string]map[string]interface{}, fetches *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/dashboards/uid/lp-dash":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"dashboard": dashboard, "meta": map[string]interface{}{}})
		case strings.HasPrefix(r.URL.Path, "/api/library-elements/"):
			fetches.Add(1)
			uid := strings.TrimPrefix(r.URL.Path, "/api/library-elements/")
			model, ok := elements[uid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"library element could not be found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(libraryElementJSON(uid, model))
		default:
			http.NotFound(w, r)
		}
	}))
}

var cpuModel = map[string]interface{}{
	"title":      "library title",
	"type":       "timeseries",
	"datasource": map[string]interface{}{"uid": "prom", "type": "prometheus"},
	"targets":    []interface{}{map[string]interface{}{"refId": "A", "expr": "rate(cpu_seconds_total[5m])"}},
}

func TestGetDashboardPanelQueries_LibraryPanels(t *testing.T) {
	dashboard := map[string]interface{}{
		"title": "lp",
		"panels": []interface{}{
			libraryPanelRef(1, "cpu top level", "lp-cpu"),
			map[string]interface{}{
				"id": float64(2), "type": "row", "title": "collapsed", "collapsed": true,
				"panels": []interface{}{libraryPanelRef(3, "", "lp-cpu")},
			},
			map[string]interface{}{
				"id": float64(4), "title": "inline", "datasource": map[string]interface{}{"uid": "prom", "type": "prometheus"},
				"targets": []interface{}{map[string]interface{}{"refId": "A", "expr": "up"}},
			},
			libraryPanelRef(5, "gone", "lp-missing"),
		},
	}
	var fetches atomic.Int32
	server := libraryServer(t, dashboard, map[string]map[string]interface{}{"lp-cpu": cpuModel}, &fetches)
	defer server.Close()
	ctx := mockSearchCtx(server)

	t.Run("resolves top-level and row-nested library panels, once per uid", func(t *testing.T) {
		fetches.Store(0)
		got, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "lp-dash"})
		require.NoError(t, err)
		require.Len(t, got, 4)

		assert.Equal(t, "cpu top level", got[0].Title, "dashboard title wins")
		assert.Equal(t, "rate(cpu_seconds_total[5m])", got[0].Query)
		assert.Equal(t, datasourceInfo{UID: "prom", Type: "prometheus"}, got[0].Datasource)
		assert.Equal(t, "A", got[0].RefID)

		assert.Equal(t, "library title", got[1].Title, "library title used when the dashboard sets none")
		assert.Equal(t, "rate(cpu_seconds_total[5m])", got[1].Query)

		assert.Equal(t, "up", got[2].Query, "inline panels are untouched")

		// 2 distinct uids (lp-cpu, lp-missing), each fetched once.
		assert.EqualValues(t, 2, fetches.Load())
	})

	t.Run("unresolvable library panel is reported, not dropped", func(t *testing.T) {
		got, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "lp-dash"})
		require.NoError(t, err)
		last := got[len(got)-1]
		assert.Equal(t, "gone", last.Title)
		require.Len(t, last.Warnings, 1)
		assert.Contains(t, last.Warnings[0], `library panel "lp-missing"`)
	})

	t.Run("panelId on a library panel resolves it", func(t *testing.T) {
		id := 3
		got, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "lp-dash", PanelID: &id})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "rate(cpu_seconds_total[5m])", got[0].Query)
	})

	t.Run("panelId on an unresolvable library panel is an error naming the uid", func(t *testing.T) {
		id := 5
		_, err := GetDashboardPanelQueriesTool(ctx, DashboardPanelQueriesParams{UID: "lp-dash", PanelID: &id})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `library panel "lp-missing"`)
	})
}

func TestGetPanelQueriesV2_LibraryPanels(t *testing.T) {
	spec := map[string]interface{}{"elements": map[string]interface{}{
		"lib": map[string]interface{}{"kind": "LibraryPanel", "spec": libraryPanelRef(2, "cpu", "lp-cpu")},
	}}
	var fetches atomic.Int32
	server := libraryServer(t, nil, map[string]map[string]interface{}{"lp-cpu": cpuModel}, &fetches)
	defer server.Close()
	ctx := mockSearchCtx(server)

	got, err := getPanelQueriesV2(ctx, spec, DashboardPanelQueriesParams{UID: "lp-dash"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "cpu", got[0].Title)
	assert.Equal(t, "rate(cpu_seconds_total[5m])", got[0].Query)
	assert.Equal(t, "prom", got[0].Datasource.UID)

	id := 2
	got, err = getPanelQueriesV2(ctx, spec, DashboardPanelQueriesParams{UID: "lp-dash", PanelID: &id})
	require.NoError(t, err)
	require.Len(t, got, 1)

	missing := 9
	_, err = getPanelQueriesV2(ctx, spec, DashboardPanelQueriesParams{UID: "lp-dash", PanelID: &missing})
	require.ErrorContains(t, err, "panel with ID 9 not found")

	// A failed fetch names the uid.
	bad := map[string]interface{}{"elements": map[string]interface{}{
		"lib": map[string]interface{}{"kind": "LibraryPanel", "spec": libraryPanelRef(2, "cpu", "lp-nope")},
	}}
	got, err = getPanelQueriesV2(ctx, bad, DashboardPanelQueriesParams{UID: "lp-dash"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Warnings[0], `library panel "lp-nope"`)
}

// runPanelQuery must run a library panel's resolved query, using the library
// element's datasource, for both dashboard schemas.
func TestRunPanelQuery_LibraryPanel(t *testing.T) {
	lokiModel := map[string]interface{}{
		"type":       "logs",
		"datasource": map[string]interface{}{"uid": "loki", "type": "loki"},
		"targets":    []interface{}{map[string]interface{}{"refId": "A", "expr": `{app="lib"}`}},
	}
	var fetches atomic.Int32
	loki, payload := lokiQueryStub()
	defer loki.Close()
	lokiURL, err := url.Parse(loki.URL)
	require.NoError(t, err)
	lib := libraryServer(t, nil, map[string]map[string]interface{}{"lp-logs": lokiModel}, &fetches)
	defer lib.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/library-elements/") {
			lib.Config.Handler.ServeHTTP(w, r)
			return
		}
		httputil.NewSingleHostReverseProxy(lokiURL).ServeHTTP(w, r)
	}))
	defer front.Close()

	run := func(t *testing.T, db map[string]interface{}, isV2 bool, id int) error {
		*payload = map[string]interface{}{}
		_, err := runSinglePanelQuery(enforceTestCtx(front, false), singlePanelQueryParams{
			DB: db, IsV2: isV2, PanelID: id, Start: "2024-01-15T10:00:00Z", End: "2024-01-15T11:00:00Z",
		})
		return err
	}
	sentExpr := func() interface{} {
		return (*payload)["queries"].([]interface{})[0].(map[string]interface{})["expr"]
	}

	t.Run("v1 dashboard", func(t *testing.T) {
		db := map[string]interface{}{"panels": []interface{}{libraryPanelRef(1, "logs", "lp-logs")}}
		require.NoError(t, run(t, db, false, 1))
		assert.Equal(t, `{app="lib"}`, sentExpr())
	})
	t.Run("v1 dashboard, panel nested in a collapsed row", func(t *testing.T) {
		db := map[string]interface{}{"panels": []interface{}{map[string]interface{}{
			"id": float64(9), "type": "row", "collapsed": true,
			"panels": []interface{}{libraryPanelRef(1, "logs", "lp-logs")},
		}}}
		require.NoError(t, run(t, db, false, 1))
		assert.Equal(t, `{app="lib"}`, sentExpr())
	})
	t.Run("v2 dashboard", func(t *testing.T) {
		db := map[string]interface{}{"elements": map[string]interface{}{
			"lib": map[string]interface{}{"kind": "LibraryPanel", "spec": libraryPanelRef(1, "logs", "lp-logs")},
		}}
		require.NoError(t, run(t, db, true, 1))
		assert.Equal(t, `{app="lib"}`, sentExpr())
	})
	t.Run("unresolvable library panel names the uid", func(t *testing.T) {
		db := map[string]interface{}{"panels": []interface{}{libraryPanelRef(1, "logs", "lp-gone")}}
		err := run(t, db, false, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `library panel "lp-gone"`)
	})
}
