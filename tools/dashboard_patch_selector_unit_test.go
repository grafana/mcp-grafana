//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panelsWithIDs builds a dashboard whose panels carry the given ids, each with
// a title "P<id>" and three targets A, B and C.
func panelsWithIDs(ids ...int) map[string]interface{} {
	panels := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		panels = append(panels, map[string]interface{}{
			"id":          float64(id),
			"title":       fmt.Sprintf("P%d", id),
			"description": fmt.Sprintf("about %d", id),
			"targets": []interface{}{
				map[string]interface{}{"refId": "A"},
				map[string]interface{}{"refId": "B"},
				map[string]interface{}{"refId": "C"},
			},
		})
	}
	return map[string]interface{}{"title": "Dash", "panels": panels}
}

func panelIDs(t *testing.T, dashboard map[string]interface{}) []int {
	t.Helper()
	panels, ok := dashboard["panels"].([]interface{})
	require.True(t, ok)
	ids := make([]int, 0, len(panels))
	for _, p := range panels {
		ids = append(ids, int(p.(map[string]interface{})["id"].(float64)))
	}
	return ids
}

func targetRefIDs(t *testing.T, dashboard map[string]interface{}, panelID int) []string {
	t.Helper()
	for _, p := range dashboard["panels"].([]interface{}) {
		panel := p.(map[string]interface{})
		if int(panel["id"].(float64)) != panelID {
			continue
		}
		var refs []string
		for _, target := range panel["targets"].([]interface{}) {
			refs = append(refs, target.(map[string]interface{})["refId"].(string))
		}
		return refs
	}
	t.Fatalf("panel %d not found", panelID)
	return nil
}

func TestApplyJSONPath_SelectorQuotedValues(t *testing.T) {
	titles := []string{
		"CPU (avg)",
		"Disk [sda]",
		"Requests (p99) [5m]",
		`It's "quoted"`,
		"a]b)c",
		"with.dot/and-slash",
		"spaces  inside",
	}
	newData := func() map[string]interface{} {
		panels := make([]interface{}, 0, len(titles)+1)
		panels = append(panels, map[string]interface{}{"id": float64(100), "title": "CPU"})
		for i, title := range titles {
			panels = append(panels, map[string]interface{}{"id": float64(i + 1), "title": title})
		}
		return map[string]interface{}{"panels": panels}
	}
	quote := func(s string, q byte) string {
		out := []byte{q}
		for i := 0; i < len(s); i++ {
			if s[i] == q || s[i] == '\\' {
				out = append(out, '\\')
			}
			out = append(out, s[i])
		}
		return string(append(out, q))
	}

	for i, title := range titles {
		for _, q := range []byte{'\'', '"'} {
			path := fmt.Sprintf("$.panels[?(@.title==%s)].description", quote(title, q))
			t.Run(path, func(t *testing.T) {
				data := newData()
				require.NoError(t, applyJSONPath(data, path, "found", false, false))
				for j, p := range data["panels"].([]interface{}) {
					panel := p.(map[string]interface{})
					if j == i+1 {
						assert.Equal(t, "found", panel["description"], "selected panel %q", title)
					} else {
						assert.NotContains(t, panel, "description", "panel %d must be untouched", j)
					}
				}
			})
		}
	}

	t.Run("quoted value is not a prefix match", func(t *testing.T) {
		data := newData()
		require.NoError(t, applyJSONPath(data, `$.panels[?(@.title=='CPU')].description`, "found", false, false))
		panels := data["panels"].([]interface{})
		assert.Equal(t, "found", panels[0].(map[string]interface{})["description"])
		assert.NotContains(t, panels[1].(map[string]interface{}), "description")
	})

	t.Run("selector with a quoted value followed by more path", func(t *testing.T) {
		data := panelsWithIDs(1, 2)
		data["panels"].([]interface{})[1].(map[string]interface{})["title"] = "Latency (p99)"
		require.NoError(t, applyJSONPath(data, `$.panels[?(@.title=='Latency (p99)')].targets[?(@.refId=="B")]`, nil, true, true))
		assert.Equal(t, []string{"A", "C"}, targetRefIDs(t, data, 2))
		assert.Equal(t, []string{"A", "B", "C"}, targetRefIDs(t, data, 1))
	})

	t.Run("bare values keep working", func(t *testing.T) {
		for _, path := range []string{
			`$.panels[?(@.title==CPU)].description`,
			`$.panels[?@.title==CPU].description`,
			`$.panels[?title==CPU].description`,
			`$.panels[?(@.id==100)].description`,
			`$.panels[?( @.id == 100 )].description`,
		} {
			data := newData()
			require.NoError(t, applyJSONPath(data, path, "found", false, false), path)
			assert.Equal(t, "found", data["panels"].([]interface{})[0].(map[string]interface{})["description"], path)
		}
	})

	t.Run("malformed and unsupported filters are still rejected", func(t *testing.T) {
		for _, path := range []string{
			`$.panels[?(@.title=='CPU")].description`,
			`$.panels[?(@.title=='CPU (avg))].description`,
			`$.panels[?(@.id>2)].description`,
			`$.panels[?(@.id==1 && @.title=='CPU')].description`,
			`$.panels[?(@.title=~'CPU.*')].description`,
		} {
			data := newData()
			err := applyJSONPath(data, path, "x", false, false)
			require.Error(t, err, path)
			for _, p := range data["panels"].([]interface{}) {
				assert.NotContains(t, p.(map[string]interface{}), "description", path)
			}
		}
	})
}

func TestApplyPatchOperations_SelectorAndIndexRemoves(t *testing.T) {
	remove := func(path string) PatchOperation { return PatchOperation{Op: "remove", Path: path} }

	t.Run("selector remove before an index remove keeps the index on its original element", func(t *testing.T) {
		// Written order would delete id 1 first and shift the array, so that
		// $.panels[2] then hits id 4 instead of id 3.
		data := panelsWithIDs(1, 2, 3, 4)
		require.NoError(t, applyPatchOperations(data, []PatchOperation{
			remove("$.panels[?(@.id==1)]"),
			remove("$.panels[2]"),
		}))
		assert.Equal(t, []int{2, 4}, panelIDs(t, data))
	})

	t.Run("index remove before a selector remove", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3, 4)
		require.NoError(t, applyPatchOperations(data, []PatchOperation{
			remove("$.panels[0]"),
			remove("$.panels[?(@.id==3)]"),
		}))
		assert.Equal(t, []int{2, 4}, panelIDs(t, data))
	})

	t.Run("every pair of one selector remove and one index remove, in both orders", func(t *testing.T) {
		ids := []int{11, 12, 13, 14, 15}
		for selIdx := range ids {
			for numIdx := range ids {
				if selIdx == numIdx {
					continue
				}
				var want []int
				for i, id := range ids {
					if i != selIdx && i != numIdx {
						want = append(want, id)
					}
				}
				bySelector := remove(fmt.Sprintf("$.panels[?(@.id==%d)]", ids[selIdx]))
				byIndex := remove(fmt.Sprintf("$.panels[%d]", numIdx))
				for _, ops := range [][]PatchOperation{{bySelector, byIndex}, {byIndex, bySelector}} {
					data := panelsWithIDs(ids...)
					require.NoError(t, applyPatchOperations(data, ops), "%v", ops)
					assert.Equal(t, want, panelIDs(t, data), "%v", ops)
				}
			}
		}
	})

	t.Run("title selector with index removes around it", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3, 4, 5)
		require.NoError(t, applyPatchOperations(data, []PatchOperation{
			remove("$.panels[1]"),
			remove("$.panels[?(@.title=='P1')]"),
			remove("$.panels[4]"),
		}))
		assert.Equal(t, []int{3, 4}, panelIDs(t, data))
	})

	t.Run("two selector removes", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3, 4)
		require.NoError(t, applyPatchOperations(data, []PatchOperation{
			remove("$.panels[?(@.id==1)]"),
			remove("$.panels[?(@.id==4)]"),
		}))
		assert.Equal(t, []int{2, 3}, panelIDs(t, data))
	})

	t.Run("nested array reached through a selector and through an index", func(t *testing.T) {
		// Both paths name the targets of the panel with id 2 (index 1).
		data := panelsWithIDs(1, 2, 3)
		require.NoError(t, applyPatchOperations(data, []PatchOperation{
			remove("$.panels[?(@.id==2)].targets[0]"),
			remove("$.panels[1].targets[2]"),
		}))
		assert.Equal(t, []string{"B"}, targetRefIDs(t, data, 2))
		assert.Equal(t, []string{"A", "B", "C"}, targetRefIDs(t, data, 1))
		assert.Equal(t, []string{"A", "B", "C"}, targetRefIDs(t, data, 3))
	})

	t.Run("selector and index naming the same element is a duplicate", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3, 4)
		err := applyPatchOperations(data, []PatchOperation{
			remove("$.panels[?(@.id==3)]"),
			remove("$.panels[2]"),
		})
		require.ErrorContains(t, err, "duplicate remove at index 2")
		assert.Equal(t, []int{1, 2, 3, 4}, panelIDs(t, data), "nothing may be removed")
	})

	t.Run("two selectors naming the same element is a duplicate", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3, 4)
		err := applyPatchOperations(data, []PatchOperation{
			remove("$.panels[?(@.id==3)]"),
			remove("$.panels[?(@.title=='P3')]"),
		})
		require.ErrorContains(t, err, "duplicate remove at index 2")
		assert.Equal(t, []int{1, 2, 3, 4}, panelIDs(t, data), "nothing may be removed")
	})

	t.Run("selector remove that matches nothing fails before anything is removed", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3, 4)
		err := applyPatchOperations(data, []PatchOperation{
			remove("$.panels[0]"),
			remove("$.panels[?(@.id==99)]"),
		})
		require.ErrorContains(t, err, "no array element has id")
		assert.Equal(t, []int{1, 2, 3, 4}, panelIDs(t, data), "nothing may be removed")
	})

	t.Run("ambiguous selector remove fails before anything is removed", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3)
		data["panels"].([]interface{})[2].(map[string]interface{})["title"] = "P1"
		err := applyPatchOperations(data, []PatchOperation{
			remove("$.panels[1]"),
			remove("$.panels[?(@.title=='P1')]"),
		})
		require.ErrorContains(t, err, "matches multiple elements")
		assert.Equal(t, []int{1, 2, 3}, panelIDs(t, data), "nothing may be removed")
	})

	t.Run("a reordered selector remove stops if its element moved", func(t *testing.T) {
		// The two replaces hand id 6 from index 5 to index 1. The removes were
		// ordered for index 5; deleting index 1 first would shift the array
		// under $.panels[3].
		data := panelsWithIDs(1, 2, 3, 4, 5, 6)
		err := applyPatchOperations(data, []PatchOperation{
			{Op: "replace", Path: "$.panels[5].id", Value: float64(60)},
			{Op: "replace", Path: "$.panels[1].id", Value: float64(6)},
			remove("$.panels[?(@.id==6)]"),
			remove("$.panels[3]"),
		})
		require.ErrorContains(t, err, "moved from panels[5] to panels[1]")
		assert.Equal(t, []int{1, 6, 3, 4, 5, 60}, panelIDs(t, data), "no panel may be removed")
	})

	t.Run("a lone selector remove follows its element", func(t *testing.T) {
		// The only remove on targets of panel 3; removing panels[0] first moves
		// that panel from index 2 to 1 and the selector still finds it.
		data := panelsWithIDs(1, 2, 3)
		require.NoError(t, applyPatchOperations(data, []PatchOperation{
			remove("$.panels[0]"),
			remove("$.panels[?(@.id==3)].targets[?(@.refId=='B')]"),
			remove("$.panels[?(@.id==2)].description"),
		}))
		assert.Equal(t, []int{2, 3}, panelIDs(t, data))
		assert.Equal(t, []string{"A", "C"}, targetRefIDs(t, data, 3))
		assert.Equal(t, []string{"A", "B", "C"}, targetRefIDs(t, data, 2))
		assert.NotContains(t, data["panels"].([]interface{})[0].(map[string]interface{}), "description")
		assert.Contains(t, data["panels"].([]interface{})[1].(map[string]interface{}), "description")
	})

	t.Run("non-remove operations keep their position", func(t *testing.T) {
		data := panelsWithIDs(1, 2, 3, 4)
		require.NoError(t, applyPatchOperations(data, []PatchOperation{
			{Op: "replace", Path: "$.panels[?(@.id==4)].title", Value: "Renamed"},
			remove("$.panels[?(@.id==1)]"),
			{Op: "replace", Path: "$.title", Value: "New"},
			remove("$.panels[1]"),
		}))
		assert.Equal(t, []int{3, 4}, panelIDs(t, data))
		assert.Equal(t, "New", data["title"])
		assert.Equal(t, "Renamed", data["panels"].([]interface{})[1].(map[string]interface{})["title"])
	})
}

func TestSortArrayRemovesDescending_ResolvesSelectors(t *testing.T) {
	data := panelsWithIDs(1, 2, 3, 4)
	planned, err := sortArrayRemovesDescending([]PatchOperation{
		{Op: "remove", Path: "$.panels[?(@.id==1)]"},
		{Op: "replace", Path: "$.title", Value: "x"},
		{Op: "remove", Path: "$.panels[2]"},
		{Op: "remove", Path: " $.panels[?(@.title=='P4')] "},
	}, data)
	require.NoError(t, err)

	var paths []string
	for _, op := range planned {
		paths = append(paths, op.Path)
	}
	assert.Equal(t, []string{" $.panels[?(@.title=='P4')] ", "$.title", "$.panels[2]", "$.panels[?(@.id==1)]"}, paths)
	assert.Equal(t, "panels[3]", planned[0].resolvedPath)
	assert.Empty(t, planned[1].resolvedPath)
	assert.Empty(t, planned[2].resolvedPath)
	assert.Equal(t, "panels[0]", planned[3].resolvedPath)
	assert.Equal(t, []int{1, 2, 3, 4}, panelIDs(t, data), "planning must not modify the dashboard")
}

// End to end through update_dashboard: the body that reaches Grafana has lost
// exactly the panels the caller named.
func TestUpdateDashboard_SelectorRemoveDoesNotShiftIndexRemove(t *testing.T) {
	var saved map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/dashboards/uid/dash-1":
			dashboard := panelsWithIDs(1, 2, 3, 4)
			dashboard["uid"] = "dash-1"
			dashboard["id"] = float64(7)
			dashboard["version"] = float64(3)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"dashboard": dashboard,
				"meta":      map[string]interface{}{"folderUid": "folder-1"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/dashboards/db":
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			var cmd struct {
				Dashboard map[string]interface{} `json:"dashboard"`
			}
			assert.NoError(t, json.Unmarshal(body, &cmd))
			saved = cmd.Dashboard
			_, _ = w.Write([]byte(`{"id":7,"uid":"dash-1","status":"success","version":4,"url":"/d/dash-1/dash"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := updateDashboard(mockCtxWithClient(server), UpdateDashboardParams{
		UID: "dash-1",
		Operations: []PatchOperation{
			{Op: "remove", Path: "$.panels[?(@.id==1)]"},
			{Op: "remove", Path: "$.panels[2]"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, saved, "the patched dashboard must be saved")
	assert.Equal(t, []int{2, 4}, panelIDs(t, saved))
}
