//go:build unit

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVizPickFixtures runs the pick on each fixture set of the renderer drop:
// results.json (an /api/ds/query results value) or frames.json (the frames of
// query A) must pick the rule and type of expected.json.
func TestVizPickFixtures(t *testing.T) {
	dir := filepath.Join("testdata", "app_datasource_pick")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	sets := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sets++
		t.Run(e.Name(), func(t *testing.T) {
			results, err := os.ReadFile(filepath.Join(dir, e.Name(), "results.json"))
			if os.IsNotExist(err) {
				frames, err := os.ReadFile(filepath.Join(dir, e.Name(), "frames.json"))
				require.NoError(t, err)
				results, err = json.Marshal(map[string]any{"A": map[string]json.RawMessage{"frames": frames}})
				require.NoError(t, err)
			} else {
				require.NoError(t, err)
			}
			var want struct {
				Pick vizPick `json:"pick"`
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name(), "expected.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(b, &want))

			rule, err := runVizPick(vizPickTable, results, []string{"A"})
			require.NoError(t, err)
			assert.Equal(t, want.Pick, vizPick{Rule: rule.ID, Type: rule.Type})
		})
	}
	assert.GreaterOrEqual(t, sets, 17)
}

// TestVizPickTableMatchesFixtures keeps the embedded table and the fixtures
// from the same renderer drop.
func TestVizPickTableMatchesFixtures(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "app_datasource_pick", "viz-pick.json"))
	require.NoError(t, err)
	assert.JSONEq(t, string(b), string(vizPickJSON))
}

func TestParseVizPickTableErrors(t *testing.T) {
	_, err := parseVizPickTable([]byte(`{"pickVersion":2,"rules":[{"id":"default","when":{}}]}`))
	assert.ErrorContains(t, err, "pickVersion 2")
	_, err = parseVizPickTable([]byte(`{"pickVersion":1,"rules":[{"id":"a","when":{"isInstant":true}}]}`))
	assert.ErrorContains(t, err, "last rule must have an empty when")
	_, err = parseVizPickTable([]byte(`{"pickVersion":1,"rules":[{"id":"a","when":{"hasFieldType.date":true}},{"id":"default","when":{}}]}`))
	assert.ErrorContains(t, err, `"hasFieldType.date" is not part of the frame summary`)
}

// TestVizFrameSummaryEnumsAndUntyped: a field with enums counts as a string,
// and a field with no type counts as other.
func TestVizFrameSummaryEnumsAndUntyped(t *testing.T) {
	results := json.RawMessage(`{"A":{"frames":[{"schema":{"fields":[{"name":"s","type":"number"},{"name":"x"}]},
		"data":{"values":[[0,1],[1,2]],"enums":[["up","down"],null]}}]}}`)
	summary, err := vizFrameSummary(results, []string{"A"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), summary["fieldCountByType.string"])
	assert.Equal(t, float64(0), summary["fieldCountByType.number"])
	assert.Equal(t, float64(1), summary["fieldCountByType.other"])
	assert.Equal(t, false, summary["isInstant"])
	assert.Equal(t, float64(2), summary["rowCountTotal"])
}
