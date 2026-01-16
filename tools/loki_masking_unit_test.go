//go:build unit

package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMaskingConfig_JSONSerialization tests that MaskingConfig can be properly serialized/deserialized
func TestMaskingConfig_JSONSerialization(t *testing.T) {
	t.Run("full config with all fields", func(t *testing.T) {
		globalReplacement := "[REDACTED]"
		config := MaskingConfig{
			BuiltinPatterns: []string{"email", "phone", "credit_card"},
			CustomPatterns: []MaskingPattern{
				{Pattern: `\d{4}-\d{4}`, Replacement: "[ID]"},
			},
			GlobalReplacement: &globalReplacement,
			HidePatternType:   true,
		}

		// Serialize to JSON
		data, err := json.Marshal(config)
		require.NoError(t, err)

		// Deserialize back
		var decoded MaskingConfig
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		// Verify
		assert.Equal(t, config.BuiltinPatterns, decoded.BuiltinPatterns)
		assert.Equal(t, len(config.CustomPatterns), len(decoded.CustomPatterns))
		assert.Equal(t, config.CustomPatterns[0].Pattern, decoded.CustomPatterns[0].Pattern)
		assert.Equal(t, config.CustomPatterns[0].Replacement, decoded.CustomPatterns[0].Replacement)
		require.NotNil(t, decoded.GlobalReplacement)
		assert.Equal(t, *config.GlobalReplacement, *decoded.GlobalReplacement)
		assert.Equal(t, config.HidePatternType, decoded.HidePatternType)
	})

	t.Run("minimal config with only builtin patterns", func(t *testing.T) {
		config := MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}

		data, err := json.Marshal(config)
		require.NoError(t, err)

		var decoded MaskingConfig
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, []string{"email"}, decoded.BuiltinPatterns)
		assert.Nil(t, decoded.CustomPatterns)
		assert.Nil(t, decoded.GlobalReplacement)
		assert.False(t, decoded.HidePatternType)
	})

	t.Run("config with only custom patterns", func(t *testing.T) {
		config := MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: `secret-\w+`},
				{Pattern: `token-[a-z0-9]+`, Replacement: "[TOKEN]"},
			},
		}

		data, err := json.Marshal(config)
		require.NoError(t, err)

		var decoded MaskingConfig
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		require.Len(t, decoded.CustomPatterns, 2)
		assert.Equal(t, `secret-\w+`, decoded.CustomPatterns[0].Pattern)
		assert.Equal(t, "", decoded.CustomPatterns[0].Replacement)
		assert.Equal(t, `token-[a-z0-9]+`, decoded.CustomPatterns[1].Pattern)
		assert.Equal(t, "[TOKEN]", decoded.CustomPatterns[1].Replacement)
	})

	t.Run("config with empty string global replacement (delete mode)", func(t *testing.T) {
		emptyStr := ""
		config := MaskingConfig{
			BuiltinPatterns:   []string{"email"},
			GlobalReplacement: &emptyStr,
		}

		data, err := json.Marshal(config)
		require.NoError(t, err)

		var decoded MaskingConfig
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		require.NotNil(t, decoded.GlobalReplacement)
		assert.Equal(t, "", *decoded.GlobalReplacement)
	})

	t.Run("empty config", func(t *testing.T) {
		config := MaskingConfig{}

		data, err := json.Marshal(config)
		require.NoError(t, err)

		var decoded MaskingConfig
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.BuiltinPatterns)
		assert.Nil(t, decoded.CustomPatterns)
		assert.Nil(t, decoded.GlobalReplacement)
		assert.False(t, decoded.HidePatternType)
	})
}

// TestMaskingPattern_JSONSerialization tests that MaskingPattern can be properly serialized/deserialized
func TestMaskingPattern_JSONSerialization(t *testing.T) {
	t.Run("pattern with replacement", func(t *testing.T) {
		pattern := MaskingPattern{
			Pattern:     `[A-Z]{2}\d{6}`,
			Replacement: "[PASSPORT]",
		}

		data, err := json.Marshal(pattern)
		require.NoError(t, err)

		var decoded MaskingPattern
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, pattern.Pattern, decoded.Pattern)
		assert.Equal(t, pattern.Replacement, decoded.Replacement)
	})

	t.Run("pattern without replacement (default)", func(t *testing.T) {
		pattern := MaskingPattern{
			Pattern: `\b\d{3}-\d{2}-\d{4}\b`,
		}

		data, err := json.Marshal(pattern)
		require.NoError(t, err)

		var decoded MaskingPattern
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, pattern.Pattern, decoded.Pattern)
		assert.Equal(t, "", decoded.Replacement)
	})

	t.Run("pattern with empty string replacement (delete mode)", func(t *testing.T) {
		pattern := MaskingPattern{
			Pattern:     `debug:.*`,
			Replacement: "",
		}

		data, err := json.Marshal(pattern)
		require.NoError(t, err)

		var decoded MaskingPattern
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, pattern.Pattern, decoded.Pattern)
		assert.Equal(t, "", decoded.Replacement)
	})
}

// TestMaskingConfig_FieldValidation tests the structure requirements
func TestMaskingConfig_FieldValidation(t *testing.T) {
	t.Run("builtin patterns accepts valid identifiers", func(t *testing.T) {
		validPatterns := []string{
			"email",
			"phone",
			"credit_card",
			"ip_address",
			"mac_address",
			"api_key",
			"jwt_token",
		}

		config := MaskingConfig{
			BuiltinPatterns: validPatterns,
		}

		// Just verify the structure holds all patterns
		assert.Equal(t, len(validPatterns), len(config.BuiltinPatterns))
		for i, p := range validPatterns {
			assert.Equal(t, p, config.BuiltinPatterns[i])
		}
	})

	t.Run("custom patterns can have regex with capture groups", func(t *testing.T) {
		config := MaskingConfig{
			CustomPatterns: []MaskingPattern{
				// Note: Back-references are NOT supported in replacement
				// This is just testing the structure can hold patterns with capture groups
				{Pattern: `user_id=(\d+)`, Replacement: "[USER_ID]"},
			},
		}

		assert.Equal(t, `user_id=(\d+)`, config.CustomPatterns[0].Pattern)
		assert.Equal(t, "[USER_ID]", config.CustomPatterns[0].Replacement)
	})

	t.Run("hide pattern type flag", func(t *testing.T) {
		config := MaskingConfig{
			BuiltinPatterns: []string{"email"},
			HidePatternType: true,
		}

		assert.True(t, config.HidePatternType)
	})
}
