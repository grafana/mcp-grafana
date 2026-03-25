//go:build unit

package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalJSONWithLimitMsg(t *testing.T) {
	t.Run("should return response size exceeded error when data exceeds limit", func(t *testing.T) {
		bytesLimit := 50
		data := []byte(`{"key":"` + strings.Repeat("x", bytesLimit) + `"`)
		t.Logf("data length: %d, bytesLimit: %d", len(data), bytesLimit)

		var result map[string]any
		err := unmarshalJSONWithLimitMsg(data, &result, bytesLimit)
		t.Logf("returned error: %v", err)

		require.Error(t, err, "should have returned an error for oversized invalid JSON")
		assert.Contains(t, err.Error(), "response size exceeds limit", "should have response size exceeded message")
	})

	t.Run("should not return response size exceeded for invalid data when within limits ", func(t *testing.T) {
		data := []byte(`{"key":"value`)
		bytesLimit := len(data) + 100
		t.Logf("data length: %d, bytesLimit: %d", len(data), bytesLimit)

		var result map[string]any
		err := unmarshalJSONWithLimitMsg(data, &result, bytesLimit)
		t.Logf("returned error: %v, result: %v", err, result)

		require.Error(t, err, "should have error for invalid JSON")
		assert.NotContains(t, err.Error(), "response size exceeds limit", "should not have response size exceeded message")
	})

	t.Run("should parse nested JSON when within limits", func(t *testing.T) {
		data := []byte(`{"user":{"name":"alice","age":30},"active":true}`)
		bytesLimit := len(data) + 200
		t.Logf("data length: %d, bytesLimit: %d", len(data), bytesLimit)

		var result map[string]any
		err := unmarshalJSONWithLimitMsg(data, &result, bytesLimit)
		t.Logf("returned error: %v, result: %v", err, result)

		require.NoError(t, err, "should have no error when nested JSON is within byte limit")
		user, ok := result["user"].(map[string]any)
		require.True(t, ok, "should have parsed user as a map")
		assert.Equal(t, "alice", user["name"], "should have parsed nested name")
		assert.Equal(t, float64(30), user["age"], "should have parsed nested age")
		assert.Equal(t, true, result["active"], "should have parsed active flag")
	})

	t.Run("should unmarshal without error for exact byte limit", func(t *testing.T) {
		data := []byte(`{"a":"b"}`)
		bytesLimit := len(data)
		t.Logf("data length: %d, bytesLimit: %d", len(data), bytesLimit)

		var result map[string]any
		err := unmarshalJSONWithLimitMsg(data, &result, bytesLimit)
		t.Logf("returned error: %v, result: %v", err, result)

		require.NoError(t, err, "should have no error when valid JSON is at exact byte limit")
		assert.Equal(t, "b", result["a"], "should have parsed values")
	})

}
