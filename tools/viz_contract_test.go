//go:build unit

package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests do not use the network. They check that the copy of the contract registry is the copy that
// viz_contract/CONTRACT pins, and that it is consistent with itself. Nothing checks the pin against the contract
// repository yet (see viz_contract/README.md).

func parsePin(t *testing.T) map[string]string {
	t.Helper()
	pin := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(contractPin), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		require.True(t, ok, "CONTRACT line %q", line)
		pin[key] = value
	}
	return pin
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestVizContractPin(t *testing.T) {
	pin := parsePin(t)
	assert.Equal(t, "grafana/viz-contract", pin["repository"])
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{40}$`), pin["commit"])
	assert.Regexp(t, regexp.MustCompile(`^\d+\.\d+\.\d+$`), pin["version"])
	// A registry file that differs from the pin was edited by hand or copied without the script.
	assert.Equal(t, pin["sha256 chart-types.json"], sum(chartTypesJSON), "chart-types.json: run scripts/sync-viz-contract.sh")
	assert.Equal(t, pin["sha256 pick.json"], sum(pickJSON), "pick.json: run scripts/sync-viz-contract.sh")
}

func TestVizContractRegistryIsConsistent(t *testing.T) {
	names := map[string]bool{}
	for _, c := range vizCharts {
		assert.False(t, names[c.Name], "chart %s is twice in chart-types.json", c.Name)
		names[c.Name] = true
		assert.NotEmpty(t, c.Fits, "chart %s has no fits", c.Name)
		assert.NotNil(t, c.Options, "chart %s has no options", c.Name)
	}
	// registry.schema.json: the when of the last rule is empty, so one rule always matches.
	require.NotEmpty(t, vizPickRules)
	assert.Empty(t, vizPickRules[len(vizPickRules)-1].When)
	for _, rule := range vizPickRules {
		assert.True(t, names[rule.Chart], "rule %s has the unknown chart %s", rule.ID, rule.Chart)
	}
}

// fixtures.6: each rule of pick.json has a fixture set. The test sends each set to the pick and makes sure that
// each rule is the first match of at least one set.
func TestVizContractEachPickRuleHasAFixture(t *testing.T) {
	dirs, err := filepath.Glob("testdata/viz-contract/*")
	require.NoError(t, err)
	hit := map[string]bool{}
	for _, dir := range dirs {
		var response struct {
			Status int `json:"status"`
			Body   struct {
				Results map[string]vizResult `json:"results"`
			} `json:"body"`
		}
		data, err := os.ReadFile(filepath.Join(dir, "response.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &response))
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
