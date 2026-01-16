//go:build unit

package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Builtin Pattern Tests (Task 2.1)
// =============================================================================

// TestBuiltinPatternConstants tests that all builtin pattern constants are defined
func TestBuiltinPatternConstants(t *testing.T) {
	t.Run("all pattern constants are defined", func(t *testing.T) {
		// Verify all constants are defined with expected values
		assert.Equal(t, BuiltinPatternID("email"), PatternEmail)
		assert.Equal(t, BuiltinPatternID("phone"), PatternPhone)
		assert.Equal(t, BuiltinPatternID("credit_card"), PatternCreditCard)
		assert.Equal(t, BuiltinPatternID("ip_address"), PatternIPAddress)
		assert.Equal(t, BuiltinPatternID("mac_address"), PatternMACAddress)
		assert.Equal(t, BuiltinPatternID("api_key"), PatternAPIKey)
		assert.Equal(t, BuiltinPatternID("jwt_token"), PatternJWTToken)
	})
}

// TestBuiltinPatternsRegistry tests that all builtin patterns are registered
func TestBuiltinPatternsRegistry(t *testing.T) {
	t.Run("all patterns are registered in builtinPatterns map", func(t *testing.T) {
		expectedPatterns := []BuiltinPatternID{
			PatternEmail,
			PatternPhone,
			PatternCreditCard,
			PatternIPAddress,
			PatternMACAddress,
			PatternAPIKey,
			PatternJWTToken,
		}

		for _, id := range expectedPatterns {
			regex, exists := builtinPatterns[id]
			assert.True(t, exists, "pattern %s should exist in builtinPatterns", id)
			assert.NotNil(t, regex, "pattern %s should have compiled regex", id)
		}
	})

	t.Run("validBuiltinPatterns contains all pattern IDs", func(t *testing.T) {
		expectedPatterns := []BuiltinPatternID{
			PatternEmail,
			PatternPhone,
			PatternCreditCard,
			PatternIPAddress,
			PatternMACAddress,
			PatternAPIKey,
			PatternJWTToken,
		}

		assert.Equal(t, len(expectedPatterns), len(validBuiltinPatterns))
		for _, id := range expectedPatterns {
			found := false
			for _, valid := range validBuiltinPatterns {
				if valid == id {
					found = true
					break
				}
			}
			assert.True(t, found, "pattern %s should be in validBuiltinPatterns", id)
		}
	})
}

// TestBuiltinPatternEmail tests the email pattern matching
func TestBuiltinPatternEmail(t *testing.T) {
	regex := builtinPatterns[PatternEmail]
	require.NotNil(t, regex)

	testCases := []struct {
		input   string
		matches bool
		desc    string
	}{
		{"user@example.com", true, "basic email"},
		{"user.name@example.co.jp", true, "email with dots and country TLD"},
		{"user+tag@example.org", true, "email with plus sign"},
		{"user_name@sub.domain.com", true, "email with underscore and subdomain"},
		{"test123@test.io", true, "alphanumeric local part"},
		{"not-an-email", false, "no @ sign"},
		{"@example.com", false, "missing local part"},
		{"user@", false, "missing domain"},
		{"user@.com", false, "missing domain name"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			match := regex.MatchString(tc.input)
			assert.Equal(t, tc.matches, match, "input: %s", tc.input)
		})
	}
}

// TestBuiltinPatternPhone tests the phone pattern matching (E.164 format only)
func TestBuiltinPatternPhone(t *testing.T) {
	regex := builtinPatterns[PatternPhone]
	require.NotNil(t, regex)

	testCases := []struct {
		input   string
		matches bool
		desc    string
	}{
		{"+819012345678", true, "Japanese mobile E.164"},
		{"+14155551234", true, "US phone E.164"},
		{"+442071234567", true, "UK phone E.164"},
		{"+1234567", true, "minimum length E.164 (7 digits)"},
		{"+123456789012345", true, "maximum length E.164 (15 digits)"},
		{"090-1234-5678", false, "Japanese local format (not supported)"},
		{"(415) 555-1234", false, "US local format (not supported)"},
		{"+0123456789", false, "E.164 cannot start with +0"},
		{"+123456", false, "too short (6 digits)"},
		// Note: +1234567890123456 (16 digits) will partially match first 15 digits
		// This is acceptable for masking - the sensitive portion is still captured
		{"+1234567890123456", true, "16 digits partially matches (captures 15)"},
		{"1234567890", false, "missing + prefix"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			match := regex.MatchString(tc.input)
			assert.Equal(t, tc.matches, match, "input: %s", tc.input)
		})
	}
}

// TestBuiltinPatternCreditCard tests the credit card pattern matching
func TestBuiltinPatternCreditCard(t *testing.T) {
	regex := builtinPatterns[PatternCreditCard]
	require.NotNil(t, regex)

	testCases := []struct {
		input   string
		matches bool
		desc    string
	}{
		{"4111111111111111", true, "Visa without separators"},
		{"4111-1111-1111-1111", true, "Visa with dashes"},
		{"4111 1111 1111 1111", true, "Visa with spaces"},
		{"5500000000000004", true, "Mastercard without separators"},
		{"3400-0000-0000-009", false, "Amex (15 digits, different format)"},
		{"1234-5678", false, "too short"},
		{"not a credit card", false, "non-numeric"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			match := regex.MatchString(tc.input)
			assert.Equal(t, tc.matches, match, "input: %s", tc.input)
		})
	}
}

// TestBuiltinPatternIPAddress tests the IP address pattern matching (IPv4 and IPv6)
func TestBuiltinPatternIPAddress(t *testing.T) {
	regex := builtinPatterns[PatternIPAddress]
	require.NotNil(t, regex)

	testCases := []struct {
		input   string
		matches bool
		desc    string
	}{
		// IPv4
		{"192.168.1.1", true, "private IPv4"},
		{"10.0.0.1", true, "private IPv4 class A"},
		{"255.255.255.255", true, "broadcast address"},
		{"0.0.0.0", true, "all zeros"},
		{"8.8.8.8", true, "Google DNS"},
		// IPv6 (full format)
		{"2001:0db8:85a3:0000:0000:8a2e:0370:7334", true, "full IPv6"},
		{"fe80:0000:0000:0000:0000:0000:0000:0001", true, "link-local IPv6"},
		// Edge cases
		// Note: 256.x.x.x contains valid pattern "56.1.1.1" so it matches for masking purposes
		// Strict IP validation is not in scope - we aim to catch IP-like patterns
		{"256.1.1.1", true, "partially matches IP-like pattern"},
		{"not an ip", false, "non-numeric"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			match := regex.MatchString(tc.input)
			assert.Equal(t, tc.matches, match, "input: %s", tc.input)
		})
	}
}

// TestBuiltinPatternMACAddress tests the MAC address pattern matching
func TestBuiltinPatternMACAddress(t *testing.T) {
	regex := builtinPatterns[PatternMACAddress]
	require.NotNil(t, regex)

	testCases := []struct {
		input   string
		matches bool
		desc    string
	}{
		{"00:1A:2B:3C:4D:5E", true, "uppercase with colons"},
		{"00:1a:2b:3c:4d:5e", true, "lowercase with colons"},
		{"00-1A-2B-3C-4D-5E", true, "uppercase with dashes"},
		{"00-1a-2b-3c-4d-5e", true, "lowercase with dashes"},
		{"001A2B3C4D5E", false, "no separators"},
		{"00:1A:2B:3C:4D", false, "too short"},
		// Note: 7-octet string contains valid 6-octet MAC pattern for masking
		{"00:1A:2B:3C:4D:5E:6F", true, "contains valid MAC pattern"},
		{"GG:HH:II:JJ:KK:LL", false, "invalid hex characters"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			match := regex.MatchString(tc.input)
			assert.Equal(t, tc.matches, match, "input: %s", tc.input)
		})
	}
}

// TestBuiltinPatternAPIKey tests the API key pattern matching
func TestBuiltinPatternAPIKey(t *testing.T) {
	regex := builtinPatterns[PatternAPIKey]
	require.NotNil(t, regex)

	testCases := []struct {
		input   string
		matches bool
		desc    string
	}{
		{"api_key=abc123def456ghi789", true, "api_key with equals"},
		{"apikey:xyz789abc123def456", true, "apikey with colon"},
		{"API_KEY=ABCDEF1234567890", true, "uppercase API_KEY"},
		{"token=abcdefghijklmnop", true, "token with equals"},
		{"secret:1234567890abcdef", true, "secret with colon"},
		{"password=verysecret123456", true, "password with equals"},
		{"auth abcdefghijklmnop1234", true, "auth with space"},
		{"api-key=test1234567890ab", true, "api-key with dash"},
		{"random=short", false, "value too short (< 16 chars)"},
		{"notakey=value", false, "unknown key name"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			match := regex.MatchString(tc.input)
			assert.Equal(t, tc.matches, match, "input: %s", tc.input)
		})
	}
}

// TestBuiltinPatternJWTToken tests the JWT token pattern matching
func TestBuiltinPatternJWTToken(t *testing.T) {
	regex := builtinPatterns[PatternJWTToken]
	require.NotNil(t, regex)

	testCases := []struct {
		input   string
		matches bool
		desc    string
	}{
		{
			"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			true,
			"valid JWT",
		},
		{
			"eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJ0ZXN0In0.signature_here",
			true,
			"minimal JWT",
		},
		{"not.a.jwt", false, "wrong format"},
		{"eyXXX.eyYYY.zzz", false, "doesn't start with eyJ"},
		{"random string", false, "no dots"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			match := regex.MatchString(tc.input)
			assert.Equal(t, tc.matches, match, "input: %s", tc.input)
		})
	}
}

// =============================================================================
// GetBuiltinPattern Tests
// =============================================================================

// TestGetBuiltinPattern tests the GetBuiltinPattern helper function
func TestGetBuiltinPattern(t *testing.T) {
	t.Run("returns regex for valid pattern ID", func(t *testing.T) {
		validIDs := []BuiltinPatternID{
			PatternEmail,
			PatternPhone,
			PatternCreditCard,
			PatternIPAddress,
			PatternMACAddress,
			PatternAPIKey,
			PatternJWTToken,
		}

		for _, id := range validIDs {
			regex, err := GetBuiltinPattern(string(id))
			require.NoError(t, err, "GetBuiltinPattern(%s) should not error", id)
			assert.NotNil(t, regex, "GetBuiltinPattern(%s) should return regex", id)
		}
	})

	t.Run("returns error for invalid pattern ID", func(t *testing.T) {
		invalidIDs := []string{"invalid", "unknown", "EMAIL", "PHONE", ""}

		for _, id := range invalidIDs {
			regex, err := GetBuiltinPattern(id)
			require.Error(t, err, "GetBuiltinPattern(%s) should error", id)
			assert.Nil(t, regex, "GetBuiltinPattern(%s) should return nil", id)
			assert.Contains(t, err.Error(), "invalid builtin pattern")
			assert.Contains(t, err.Error(), id)
		}
	})
}

// TestIsValidBuiltinPattern tests the IsValidBuiltinPattern helper function
func TestIsValidBuiltinPattern(t *testing.T) {
	t.Run("returns true for valid pattern IDs", func(t *testing.T) {
		validIDs := []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		}

		for _, id := range validIDs {
			assert.True(t, IsValidBuiltinPattern(id), "IsValidBuiltinPattern(%s) should be true", id)
		}
	})

	t.Run("returns false for invalid pattern IDs", func(t *testing.T) {
		invalidIDs := []string{
			"invalid", "unknown", "EMAIL", "PHONE", "",
			"email ", " email", "Email",
		}

		for _, id := range invalidIDs {
			assert.False(t, IsValidBuiltinPattern(id), "IsValidBuiltinPattern(%s) should be false", id)
		}
	})
}

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
