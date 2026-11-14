//go:build unit

package tools

import (
	"encoding/json"
	"io/fs"
	"testing"

	vizcontract "github.com/grafana/viz-contract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONArrayEach(t *testing.T) {
	for raw, want := range map[string][]string{
		`[]`:                      nil,
		` [ ] `:                   nil,
		`[1]`:                     {"1"},
		`[1, null ,-2.5e3]`:       {"1", "null", "-2.5e3"},
		`["a,b", "c\"]d", "e\\"]`: {`"a,b"`, `"c\"]d"`, `"e\\"`},
		`[[1,2],{"a":[3,4]},[]]`:  {"[1,2]", `{"a":[3,4]}`, "[]"},
		`null`:                    nil,
		`{"a":1}`:                 nil,
		`"[1,2]"`:                 nil,
		`5`:                       nil,
	} {
		var got []string
		n := jsonArrayEach(json.RawMessage(raw), func(elem []byte) bool {
			got = append(got, string(elem))
			return true
		})
		assert.Equal(t, want, got, raw)
		assert.Equal(t, len(want), n, raw)
		assert.Equal(t, len(want), jsonArrayEach(json.RawMessage(raw), nil), raw)
	}
}

func TestJSONArrayEachStops(t *testing.T) {
	var seen []string
	jsonArrayEach(json.RawMessage(`[1,2,3,4]`), func(elem []byte) bool {
		seen = append(seen, string(elem))
		return len(seen) < 2
	})
	assert.Equal(t, []string{"1", "2"}, seen)
}

func TestIsInstant(t *testing.T) {
	frame := func(times string) map[string]vizResult {
		var results map[string]vizResult
		body := `{"A":{"frames":[{"schema":{"fields":[{"name":"Time","type":"time"},{"name":"Value","type":"number"}]},"data":{"values":[` + times + `,[1,2,3]]}}]}}`
		if err := json.Unmarshal([]byte(body), &results); err != nil {
			t.Fatal(err)
		}
		return results
	}
	assert.Equal(t, true, vizSummary(frame(`[5,5,5]`))["isInstant"])
	assert.Equal(t, true, vizSummary(frame(`[null,5,5]`))["isInstant"])
	assert.Equal(t, false, vizSummary(frame(`[5,6,5]`))["isInstant"])
	assert.Equal(t, false, vizSummary(frame(`[null,null,null]`))["isInstant"])
	assert.Equal(t, 3, vizSummary(frame(`[5,6,5]`))["rowCountTotal"])
}

// The Go constants are those of registry/constants.json of the pinned contract.
func TestVizContractConstants(t *testing.T) {
	data, err := fs.ReadFile(vizcontract.Registry(), "constants.json")
	require.NoError(t, err)
	var constants map[string]struct{ Value any }
	require.NoError(t, json.Unmarshal(data, &constants))
	for name, got := range map[string]any{
		"defaultFrom":          defaultDSFrom,
		"defaultTo":            defaultDSTo,
		"defaultMaxDataPoints": float64(defaultDSMaxDataPoints),
		"maxResultBytes":       float64(maxDSResultBytes),
		"maxTitleChars":        float64(maxDSTitleChars),
		"maxAppNotes":          float64(maxAppNotes),
		"maxAppNoteChars":      float64(maxAppNoteChars),
	} {
		c, ok := constants[name]
		require.True(t, ok, name)
		assert.Equal(t, c.Value, got, name)
	}
}

func TestVizContractRegistryIsConsistent(t *testing.T) {
	names := map[string]bool{}
	for _, c := range vizCharts {
		assert.False(t, names[c.Name], "chart %s is twice in chart-types.json", c.Name)
		names[c.Name] = true
		assert.NotEmpty(t, c.Fits, "chart %s has no fits", c.Name)
		assert.NotNil(t, c.Options, "chart %s has no options", c.Name)
	}
	for _, rule := range vizPickRules {
		assert.True(t, names[rule.Chart], "rule %s has the unknown chart %s", rule.ID, rule.Chart)
	}
}

// fixtures.6: each rule of pick.json is the first match of at least one fixture set.
func TestVizContractEachPickRuleHasAFixture(t *testing.T) {
	hit := map[string]bool{}
	for _, dir := range vizSourceFixtures(t) {
		var response struct {
			Status int `json:"status"`
			Body   struct {
				Results map[string]vizResult `json:"results"`
			} `json:"body"`
		}
		readFixtureJSON(t, dir, "response.json", &response)
		if response.Status != 200 {
			continue
		}
		if rule := vizMatchingRule(vizSummary(response.Body.Results)); rule != nil {
			hit[rule.ID] = true
		}
	}
	for _, rule := range vizPickRules {
		assert.True(t, hit[rule.ID], "no fixture set reaches the rule %s", rule.ID)
	}
}
