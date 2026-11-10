//go:build unit

package tools

import (
	"fmt"
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
