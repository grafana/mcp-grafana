package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test for trailing whitespace in paths (bug fix)
func TestApplyJSONPath_TrailingWhitespace(t *testing.T) {
	t.Run("append with trailing space works", func(t *testing.T) {
		data := map[string]interface{}{
			"panels": []interface{}{"a", "b"},
		}
		// Path with trailing space - should be trimmed
		err := applyJSONPath(data, "$.panels/- ", "c", false)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "b", "c"}, data["panels"])
	})

	t.Run("path with leading and trailing whitespace", func(t *testing.T) {
		data := map[string]interface{}{
			"title": "old",
		}
		err := applyJSONPath(data, "  $.title  ", "new", false)
		require.NoError(t, err)
		assert.Equal(t, "new", data["title"])
	})
}
