//go:build unit

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCatalogWriteGateMatchesReadOnlyHint(t *testing.T) {
	for _, c := range catalog {
		for _, spec := range c.tools {
			for _, v := range spec.variants {
				readOnly := v.tool.Tool.Annotations != nil && v.tool.Tool.Annotations.ReadOnlyHint
				assert.Equal(t, v.gate&gateWrite == 0, readOnly,
					"%s/%s: only a write-gated variant may declare readOnlyHint=false", c.name, v.tool.Tool.Name)
			}
		}
	}
}

func TestCatalogNamesEachToolOnce(t *testing.T) {
	seen := map[string]string{}
	viewers := 0
	var lifts []string
	for _, c := range catalog {
		for _, spec := range c.tools {
			if spec.kind == appViewer {
				viewers++
				continue
			}
			name := spec.variants[0].tool.Tool.Name
			for _, v := range spec.variants {
				assert.Equal(t, name, v.tool.Tool.Name, "%s: variants of one tool must share its name", c.name)
				lifts = append(lifts, v.liftedBy...)
			}
			if other, ok := seen[name]; ok {
				t.Errorf("%s is listed in both %s and %s", name, other, c.name)
			}
			seen[name] = c.name
		}
	}
	assert.Equal(t, 1, viewers, "render_grafana_app needs exactly one host category")
	for _, name := range lifts {
		assert.Contains(t, seen, name, "liftedBy names a tool that is not in the catalog")
	}
}
