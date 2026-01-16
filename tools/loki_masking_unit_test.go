//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Builtin Pattern Tests
// =============================================================================

func TestBuiltinPatternConstants(t *testing.T) {
	t.Run("all pattern constants are defined", func(t *testing.T) {
		assert.Equal(t, BuiltinPatternID("email"), PatternEmail)
		assert.Equal(t, BuiltinPatternID("phone"), PatternPhone)
		assert.Equal(t, BuiltinPatternID("credit_card"), PatternCreditCard)
		assert.Equal(t, BuiltinPatternID("ip_address"), PatternIPAddress)
		assert.Equal(t, BuiltinPatternID("mac_address"), PatternMACAddress)
		assert.Equal(t, BuiltinPatternID("api_key"), PatternAPIKey)
		assert.Equal(t, BuiltinPatternID("jwt_token"), PatternJWTToken)
	})
}

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

// =============================================================================
// ValidateMaskingConfig Tests
// =============================================================================

func TestValidateMaskingConfig_ValidConfig(t *testing.T) {
	testCases := []struct {
		name   string
		config *MaskingConfig
	}{
		{
			name:   "nil config is valid (no masking)",
			config: nil,
		},
		{
			name:   "empty config is valid",
			config: &MaskingConfig{},
		},
		{
			name: "config with single builtin pattern",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"email"},
			},
		},
		{
			name: "config with all builtin patterns",
			config: &MaskingConfig{
				BuiltinPatterns: []string{
					"email", "phone", "credit_card", "ip_address",
					"mac_address", "api_key", "jwt_token",
				},
			},
		},
		{
			name: "config with single custom pattern",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `\d{4}-\d{4}`},
				},
			},
		},
		{
			name: "config with builtin and custom patterns",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"email", "phone"},
				CustomPatterns: []MaskingPattern{
					{Pattern: `secret-\w+`},
				},
			},
		},
		{
			name: "config at max pattern limit (20)",
			config: &MaskingConfig{
				BuiltinPatterns: []string{
					"email", "phone", "credit_card", "ip_address",
					"mac_address", "api_key", "jwt_token",
				},
				CustomPatterns: []MaskingPattern{
					{Pattern: `pattern1`},
					{Pattern: `pattern2`},
					{Pattern: `pattern3`},
					{Pattern: `pattern4`},
					{Pattern: `pattern5`},
					{Pattern: `pattern6`},
					{Pattern: `pattern7`},
					{Pattern: `pattern8`},
					{Pattern: `pattern9`},
					{Pattern: `pattern10`},
					{Pattern: `pattern11`},
					{Pattern: `pattern12`},
					{Pattern: `pattern13`},
				},
			},
		},
		{
			name: "config with complex valid regex",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `(?i)password[=:\s]["']?[a-zA-Z0-9_\-]{8,}`},
					{Pattern: `\b[A-Z]{2}\d{6}[A-Z]?\b`},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMaskingConfig(tc.config)
			assert.NoError(t, err)
		})
	}
}

func TestValidateMaskingConfig_InvalidBuiltinPattern(t *testing.T) {
	testCases := []struct {
		name           string
		config         *MaskingConfig
		expectedErrMsg string
	}{
		{
			name: "unknown pattern identifier",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"unknown"},
			},
			expectedErrMsg: "invalid builtin pattern identifier",
		},
		{
			name: "case-sensitive - uppercase EMAIL",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"EMAIL"},
			},
			expectedErrMsg: "invalid builtin pattern identifier",
		},
		{
			name: "mixed valid and invalid patterns",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"email", "invalid", "phone"},
			},
			expectedErrMsg: "invalid builtin pattern identifier",
		},
		{
			name: "empty string as pattern identifier",
			config: &MaskingConfig{
				BuiltinPatterns: []string{""},
			},
			expectedErrMsg: "invalid builtin pattern identifier",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMaskingConfig(tc.config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErrMsg)
			// Should include available patterns in error
			assert.Contains(t, err.Error(), "available")
		})
	}
}

func TestValidateMaskingConfig_InvalidRegex(t *testing.T) {
	testCases := []struct {
		name           string
		config         *MaskingConfig
		expectedErrMsg string
	}{
		{
			name: "invalid regex - unclosed bracket",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `[a-z`},
				},
			},
			expectedErrMsg: "invalid regex pattern",
		},
		{
			name: "invalid regex - unclosed parenthesis",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `(abc`},
				},
			},
			expectedErrMsg: "invalid regex pattern",
		},
		{
			name: "invalid regex - bad escape",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `\`},
				},
			},
			expectedErrMsg: "invalid regex pattern",
		},
		{
			name: "invalid regex - invalid quantifier",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `a{2,1}`}, // min > max
				},
			},
			expectedErrMsg: "invalid regex pattern",
		},
		{
			name: "mixed valid and invalid regex",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `valid-pattern`},
					{Pattern: `[invalid`},
				},
			},
			expectedErrMsg: "invalid regex pattern",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMaskingConfig(tc.config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErrMsg)
		})
	}
}

func TestValidateMaskingConfig_TooManyPatterns(t *testing.T) {
	t.Run("exceeds max pattern limit with builtin only", func(t *testing.T) {
		// Create 21 builtin patterns (only 7 valid, but test the count check)
		patterns := make([]string, 21)
		for i := 0; i < 21; i++ {
			patterns[i] = "email" // duplicate, but count is what matters
		}
		config := &MaskingConfig{
			BuiltinPatterns: patterns,
		}

		err := ValidateMaskingConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many")
		assert.Contains(t, err.Error(), "20")
	})

	t.Run("exceeds max pattern limit with custom only", func(t *testing.T) {
		patterns := make([]MaskingPattern, 21)
		for i := 0; i < 21; i++ {
			patterns[i] = MaskingPattern{Pattern: fmt.Sprintf("pattern%d", i)}
		}
		config := &MaskingConfig{
			CustomPatterns: patterns,
		}

		err := ValidateMaskingConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many")
	})

	t.Run("exceeds max pattern limit with combined builtin and custom", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{
				"email", "phone", "credit_card", "ip_address",
				"mac_address", "api_key", "jwt_token",
			},
			CustomPatterns: []MaskingPattern{
				{Pattern: `pattern1`},
				{Pattern: `pattern2`},
				{Pattern: `pattern3`},
				{Pattern: `pattern4`},
				{Pattern: `pattern5`},
				{Pattern: `pattern6`},
				{Pattern: `pattern7`},
				{Pattern: `pattern8`},
				{Pattern: `pattern9`},
				{Pattern: `pattern10`},
				{Pattern: `pattern11`},
				{Pattern: `pattern12`},
				{Pattern: `pattern13`},
				{Pattern: `pattern14`}, // This makes 21 total (7 + 14)
			},
		}

		err := ValidateMaskingConfig(config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many")
	})
}

func TestValidateMaskingConfig_VeryLongRegex(t *testing.T) {
	t.Run("accepts very long valid regex", func(t *testing.T) {
		// Create a long but valid regex
		longPattern := strings.Repeat(`[a-z]`, 100)
		config := &MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: longPattern},
			},
		}

		err := ValidateMaskingConfig(config)
		assert.NoError(t, err)
	})
}

// =============================================================================
// NewLogMasker Tests
// =============================================================================

func TestNewLogMasker_ValidConfig(t *testing.T) {
	testCases := []struct {
		name   string
		config *MaskingConfig
	}{
		{
			name:   "nil config returns nil masker",
			config: nil,
		},
		{
			name:   "empty config returns masker with no patterns",
			config: &MaskingConfig{},
		},
		{
			name: "config with builtin patterns",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"email", "phone"},
			},
		},
		{
			name: "config with custom patterns",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `\d{4}-\d{4}`},
				},
			},
		},
		{
			name: "config with builtin and custom patterns",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"email"},
				CustomPatterns: []MaskingPattern{
					{Pattern: `secret-\w+`, Replacement: "[SECRET]"},
				},
			},
		},
		{
			name: "config with global replacement",
			config: &MaskingConfig{
				BuiltinPatterns:   []string{"email"},
				GlobalReplacement: strPtr("[REDACTED]"),
			},
		},
		{
			name: "config with hide pattern type",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"email"},
				HidePatternType: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			masker, err := NewLogMasker(tc.config)
			assert.NoError(t, err)
			if tc.config == nil {
				assert.Nil(t, masker)
			} else {
				assert.NotNil(t, masker)
			}
		})
	}
}

func TestNewLogMasker_InvalidConfig(t *testing.T) {
	testCases := []struct {
		name           string
		config         *MaskingConfig
		expectedErrMsg string
	}{
		{
			name: "invalid builtin pattern",
			config: &MaskingConfig{
				BuiltinPatterns: []string{"invalid"},
			},
			expectedErrMsg: "invalid builtin pattern",
		},
		{
			name: "invalid regex pattern",
			config: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `[invalid`},
				},
			},
			expectedErrMsg: "invalid regex pattern",
		},
		{
			name: "too many patterns",
			config: &MaskingConfig{
				BuiltinPatterns: make([]string, 21), // Will fail count check
			},
			expectedErrMsg: "too many",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			masker, err := NewLogMasker(tc.config)
			require.Error(t, err)
			assert.Nil(t, masker)
			assert.Contains(t, err.Error(), tc.expectedErrMsg)
		})
	}
}

func TestNewLogMasker_PatternOrder(t *testing.T) {
	t.Run("builtin patterns come before custom patterns", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email", "phone"},
			CustomPatterns: []MaskingPattern{
				{Pattern: `custom1`},
				{Pattern: `custom2`},
			},
		}

		masker, err := NewLogMasker(config)
		require.NoError(t, err)
		require.NotNil(t, masker)

		// Verify pattern count: 2 builtin + 2 custom = 4
		assert.Equal(t, 4, masker.PatternCount())
	})
}

func TestNewLogMasker_GlobalReplacementHandling(t *testing.T) {
	t.Run("nil global replacement uses pattern-specific", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}

		masker, err := NewLogMasker(config)
		require.NoError(t, err)
		assert.False(t, masker.HasGlobalReplacement())
	})

	t.Run("empty string global replacement for deletion", func(t *testing.T) {
		emptyStr := ""
		config := &MaskingConfig{
			BuiltinPatterns:   []string{"email"},
			GlobalReplacement: &emptyStr,
		}

		masker, err := NewLogMasker(config)
		require.NoError(t, err)
		assert.True(t, masker.HasGlobalReplacement())
	})

	t.Run("non-empty global replacement", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns:   []string{"email"},
			GlobalReplacement: strPtr("[REDACTED]"),
		}

		masker, err := NewLogMasker(config)
		require.NoError(t, err)
		assert.True(t, masker.HasGlobalReplacement())
	})
}

// strPtr is a helper function to create a pointer to a string
func strPtr(s string) *string {
	return &s
}

// =============================================================================
// MaskEntries Tests
// =============================================================================

func TestLogMasker_MaskEntries_BuiltinPatterns(t *testing.T) {
	testCases := []struct {
		name     string
		patterns []string
		input    string
		expected string
	}{
		{
			name:     "email pattern",
			patterns: []string{"email"},
			input:    "User login: user@example.com",
			expected: "User login: [MASKED:email]",
		},
		{
			name:     "phone pattern (E.164)",
			patterns: []string{"phone"},
			input:    "Contact: +819012345678",
			expected: "Contact: [MASKED:phone]",
		},
		{
			name:     "credit card pattern",
			patterns: []string{"credit_card"},
			input:    "Payment with 4111-1111-1111-1111",
			expected: "Payment with [MASKED:credit_card]",
		},
		{
			name:     "ip address pattern (IPv4)",
			patterns: []string{"ip_address"},
			input:    "Server IP: 192.168.1.100",
			expected: "Server IP: [MASKED:ip_address]",
		},
		{
			name:     "mac address pattern",
			patterns: []string{"mac_address"},
			input:    "Device MAC: 00:1A:2B:3C:4D:5E",
			expected: "Device MAC: [MASKED:mac_address]",
		},
		{
			name:     "api key pattern",
			patterns: []string{"api_key"},
			input:    "api_key=abc123def456ghi789jkl",
			expected: "[MASKED:api_key]",
		},
		{
			name:     "jwt token pattern",
			patterns: []string{"jwt_token"},
			input:    "Token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature",
			expected: "Token: [MASKED:jwt_token]",
		},
		{
			name:     "multiple emails in one line",
			patterns: []string{"email"},
			input:    "From: sender@mail.com To: receiver@mail.com",
			expected: "From: [MASKED:email] To: [MASKED:email]",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &MaskingConfig{
				BuiltinPatterns: tc.patterns,
			}
			masker, err := NewLogMasker(config)
			require.NoError(t, err)

			entries := []LogEntry{{Line: tc.input}}
			result := masker.MaskEntries(entries)

			require.Len(t, result, 1)
			assert.Equal(t, tc.expected, result[0].Line)
		})
	}
}

func TestLogMasker_MaskEntries_CustomPatterns(t *testing.T) {
	testCases := []struct {
		name     string
		patterns []MaskingPattern
		input    string
		expected string
	}{
		{
			name: "simple custom pattern without replacement",
			patterns: []MaskingPattern{
				{Pattern: `secret-\w+`},
			},
			input:    "Found secret-abc123 in log",
			expected: "Found [MASKED:custom] in log",
		},
		{
			name: "custom pattern with replacement",
			patterns: []MaskingPattern{
				{Pattern: `user_id=\d+`, Replacement: "[USER_ID]"},
			},
			input:    "Request from user_id=12345",
			expected: "Request from [USER_ID]",
		},
		{
			name: "multiple custom patterns",
			patterns: []MaskingPattern{
				{Pattern: `secret-\w+`, Replacement: "[SECRET]"},
				{Pattern: `token-[a-z0-9]+`, Replacement: "[TOKEN]"},
			},
			input:    "secret-xyz123 and token-abc456",
			expected: "[SECRET] and [TOKEN]",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &MaskingConfig{
				CustomPatterns: tc.patterns,
			}
			masker, err := NewLogMasker(config)
			require.NoError(t, err)

			entries := []LogEntry{{Line: tc.input}}
			result := masker.MaskEntries(entries)

			require.Len(t, result, 1)
			assert.Equal(t, tc.expected, result[0].Line)
		})
	}
}

func TestLogMasker_MaskEntries_PatternTypeDisplay(t *testing.T) {
	t.Run("default shows pattern type", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
			HidePatternType: false,
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Email: user@example.com"}}
		result := masker.MaskEntries(entries)

		assert.Contains(t, result[0].Line, "[MASKED:email]")
	})

	t.Run("hide pattern type uses generic mask", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
			HidePatternType: true,
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Email: user@example.com"}}
		result := masker.MaskEntries(entries)

		assert.Contains(t, result[0].Line, "[MASKED]")
		assert.NotContains(t, result[0].Line, "[MASKED:email]")
	})

	t.Run("hide pattern type affects custom patterns too", func(t *testing.T) {
		config := &MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: `secret-\w+`}, // No explicit replacement
			},
			HidePatternType: true,
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Found secret-abc123"}}
		result := masker.MaskEntries(entries)

		assert.Contains(t, result[0].Line, "[MASKED]")
		assert.NotContains(t, result[0].Line, "[MASKED:custom]")
	})
}

func TestLogMasker_MaskEntries_GlobalReplacement(t *testing.T) {
	t.Run("global replacement overrides all patterns", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email", "phone"},
			CustomPatterns: []MaskingPattern{
				{Pattern: `secret-\w+`, Replacement: "[SECRET]"},
			},
			GlobalReplacement: strPtr("[REDACTED]"),
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "email@test.com +819012345678 secret-abc"}}
		result := masker.MaskEntries(entries)

		// All should be replaced with global replacement
		assert.Equal(t, "[REDACTED] [REDACTED] [REDACTED]", result[0].Line)
	})
}

func TestLogMasker_MaskEntries_EmptyReplacement(t *testing.T) {
	t.Run("empty global replacement deletes matches", func(t *testing.T) {
		emptyStr := ""
		config := &MaskingConfig{
			BuiltinPatterns:   []string{"email"},
			GlobalReplacement: &emptyStr,
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Contact: user@example.com for info"}}
		result := masker.MaskEntries(entries)

		assert.Equal(t, "Contact:  for info", result[0].Line)
	})

	t.Run("empty custom replacement uses default mask", func(t *testing.T) {
		// Note: Empty string in MaskingPattern.Replacement defaults to [MASKED:custom]
		// Deletion via empty replacement is only available through GlobalReplacement
		config := &MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: `debug:.*`, Replacement: ""}, // Defaults to [MASKED:custom]
			},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Info message debug: some debug info"}}
		result := masker.MaskEntries(entries)

		assert.Equal(t, "Info message [MASKED:custom]", result[0].Line)
	})

	t.Run("deletion via global replacement works for custom patterns", func(t *testing.T) {
		emptyStr := ""
		config := &MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: `\s*debug:.*`},
			},
			GlobalReplacement: &emptyStr,
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Info message debug: some debug info"}}
		result := masker.MaskEntries(entries)

		assert.Equal(t, "Info message", result[0].Line)
	})
}

func TestLogMasker_MaskEntries_PatternOrder(t *testing.T) {
	t.Run("builtin patterns applied before custom patterns", func(t *testing.T) {
		// The email pattern should match first, so custom pattern won't find email
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
			CustomPatterns: []MaskingPattern{
				{Pattern: `user@example\.com`, Replacement: "[SPECIFIC_EMAIL]"},
			},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Contact: user@example.com"}}
		result := masker.MaskEntries(entries)

		// Email builtin pattern should match first
		assert.Contains(t, result[0].Line, "[MASKED:email]")
		assert.NotContains(t, result[0].Line, "[SPECIFIC_EMAIL]")
	})
}

func TestLogMasker_MaskEntries_NoBackReference(t *testing.T) {
	t.Run("$1 in replacement is literal", func(t *testing.T) {
		config := &MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: `user_(\d+)`, Replacement: "user_$1_masked"},
			},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "Found user_12345"}}
		result := masker.MaskEntries(entries)

		// $1 should be literal, not a back-reference
		// The replacement uses ReplaceAllLiteralString which treats $1 as literal
		assert.Equal(t, "Found user_$1_masked", result[0].Line)
	})
}

func TestLogMasker_MaskEntries_EmptyConfig(t *testing.T) {
	t.Run("empty config does not modify entries", func(t *testing.T) {
		config := &MaskingConfig{}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		original := "This contains user@example.com and 192.168.1.1"
		entries := []LogEntry{{Line: original}}
		result := masker.MaskEntries(entries)

		assert.Equal(t, original, result[0].Line)
	})

	t.Run("nil masker returns unchanged entries", func(t *testing.T) {
		var masker *LogMasker = nil

		original := "This contains user@example.com"
		entries := []LogEntry{{Line: original}}
		result := masker.MaskEntries(entries)

		assert.Equal(t, original, result[0].Line)
	})
}

func TestLogMasker_MaskEntries_IPv6Address(t *testing.T) {
	config := &MaskingConfig{
		BuiltinPatterns: []string{"ip_address"},
	}
	masker, err := NewLogMasker(config)
	require.NoError(t, err)

	t.Run("full IPv6 address", func(t *testing.T) {
		entries := []LogEntry{{Line: "Server: 2001:0db8:85a3:0000:0000:8a2e:0370:7334"}}
		result := masker.MaskEntries(entries)

		assert.Contains(t, result[0].Line, "[MASKED:ip_address]")
	})
}

func TestLogMasker_MaskEntries_EdgeCases(t *testing.T) {
	t.Run("empty log line", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: ""}}
		result := masker.MaskEntries(entries)

		assert.Equal(t, "", result[0].Line)
	})

	t.Run("empty entries slice", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{}
		result := masker.MaskEntries(entries)

		assert.Empty(t, result)
	})

	t.Run("unicode content preserved", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "メッセージ: user@example.com から送信 🎉"}}
		result := masker.MaskEntries(entries)

		assert.Contains(t, result[0].Line, "メッセージ:")
		assert.Contains(t, result[0].Line, "[MASKED:email]")
		assert.Contains(t, result[0].Line, "から送信 🎉")
	})

	t.Run("multiple entries processed", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{
			{Line: "First: a@b.com"},
			{Line: "Second: no email here"},
			{Line: "Third: c@d.org"},
		}
		result := masker.MaskEntries(entries)

		require.Len(t, result, 3)
		assert.Contains(t, result[0].Line, "[MASKED:email]")
		assert.Equal(t, "Second: no email here", result[1].Line)
		assert.Contains(t, result[2].Line, "[MASKED:email]")
	})

	t.Run("preserves other entry fields", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{
			{
				Timestamp: "2024-01-01T00:00:00Z",
				Line:      "user@example.com",
				Labels:    map[string]string{"app": "test"},
			},
		}
		result := masker.MaskEntries(entries)

		require.Len(t, result, 1)
		assert.Equal(t, "2024-01-01T00:00:00Z", result[0].Timestamp)
		assert.Equal(t, map[string]string{"app": "test"}, result[0].Labels)
		assert.Contains(t, result[0].Line, "[MASKED:email]")
	})
}

func TestLogMasker_MaskEntries_LocalPhoneNotMatched(t *testing.T) {
	config := &MaskingConfig{
		BuiltinPatterns: []string{"phone"},
	}
	masker, err := NewLogMasker(config)
	require.NoError(t, err)

	testCases := []struct {
		name  string
		input string
	}{
		{"Japanese local format with dashes", "090-1234-5678"},
		{"Japanese local format without dashes", "09012345678"},
		{"US local format", "(415) 555-1234"},
		{"US format with dashes", "415-555-1234"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entries := []LogEntry{{Line: fmt.Sprintf("Contact: %s", tc.input)}}
			result := masker.MaskEntries(entries)

			// Local formats should NOT be masked by the phone builtin pattern
			assert.NotContains(t, result[0].Line, "[MASKED:phone]")
			assert.Contains(t, result[0].Line, tc.input)
		})
	}
}

func TestLogMasker_MaskEntries_E164PhoneNumber(t *testing.T) {
	config := &MaskingConfig{
		BuiltinPatterns: []string{"phone"},
	}
	masker, err := NewLogMasker(config)
	require.NoError(t, err)

	testCases := []struct {
		name  string
		input string
	}{
		{"Japanese mobile E.164", "+819012345678"},
		{"US E.164", "+14155551234"},
		{"UK E.164", "+442071234567"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entries := []LogEntry{{Line: fmt.Sprintf("Contact: %s", tc.input)}}
			result := masker.MaskEntries(entries)

			assert.Contains(t, result[0].Line, "[MASKED:phone]")
		})
	}
}

func TestLogMasker_MaskEntries_OverlappingPatterns(t *testing.T) {
	t.Run("first pattern wins on same text", func(t *testing.T) {
		config := &MaskingConfig{
			BuiltinPatterns: []string{"email"},
			CustomPatterns: []MaskingPattern{
				{Pattern: `@example\.com`, Replacement: "[DOMAIN]"},
			},
		}
		masker, err := NewLogMasker(config)
		require.NoError(t, err)

		entries := []LogEntry{{Line: "user@example.com"}}
		result := masker.MaskEntries(entries)

		// Email pattern matches first and replaces the whole thing
		assert.Equal(t, "[MASKED:email]", result[0].Line)
	})
}

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

// =============================================================================
// Loki Query Integration Tests
// =============================================================================

func TestQueryLokiLogsParams_MaskingField(t *testing.T) {
	t.Run("params with nil masking config", func(t *testing.T) {
		params := QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{app="test"}`,
			Masking:       nil,
		}

		// Serialize to JSON
		data, err := json.Marshal(params)
		require.NoError(t, err)

		// Deserialize back
		var decoded QueryLokiLogsParams
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "loki", decoded.DatasourceUID)
		assert.Equal(t, `{app="test"}`, decoded.LogQL)
		assert.Nil(t, decoded.Masking)
	})

	t.Run("params with masking config - builtin patterns", func(t *testing.T) {
		params := QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{app="test"}`,
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email", "phone"},
			},
		}

		// Serialize to JSON
		data, err := json.Marshal(params)
		require.NoError(t, err)

		// Deserialize back
		var decoded QueryLokiLogsParams
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		require.NotNil(t, decoded.Masking)
		assert.Equal(t, []string{"email", "phone"}, decoded.Masking.BuiltinPatterns)
	})

	t.Run("params with masking config - custom patterns", func(t *testing.T) {
		params := QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{app="test"}`,
			Masking: &MaskingConfig{
				CustomPatterns: []MaskingPattern{
					{Pattern: `secret-\w+`, Replacement: "[SECRET]"},
				},
			},
		}

		// Serialize to JSON
		data, err := json.Marshal(params)
		require.NoError(t, err)

		// Deserialize back
		var decoded QueryLokiLogsParams
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		require.NotNil(t, decoded.Masking)
		require.Len(t, decoded.Masking.CustomPatterns, 1)
		assert.Equal(t, `secret-\w+`, decoded.Masking.CustomPatterns[0].Pattern)
		assert.Equal(t, "[SECRET]", decoded.Masking.CustomPatterns[0].Replacement)
	})

	t.Run("params with full masking config", func(t *testing.T) {
		globalReplacement := "[REDACTED]"
		params := QueryLokiLogsParams{
			DatasourceUID: "loki",
			LogQL:         `{app="test"}`,
			Limit:         50,
			Direction:     "forward",
			Masking: &MaskingConfig{
				BuiltinPatterns: []string{"email", "credit_card"},
				CustomPatterns: []MaskingPattern{
					{Pattern: `token-[a-z0-9]+`},
				},
				GlobalReplacement: &globalReplacement,
				HidePatternType:   true,
			},
		}

		// Serialize to JSON
		data, err := json.Marshal(params)
		require.NoError(t, err)

		// Deserialize back
		var decoded QueryLokiLogsParams
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "loki", decoded.DatasourceUID)
		assert.Equal(t, `{app="test"}`, decoded.LogQL)
		assert.Equal(t, 50, decoded.Limit)
		assert.Equal(t, "forward", decoded.Direction)

		require.NotNil(t, decoded.Masking)
		assert.Equal(t, []string{"email", "credit_card"}, decoded.Masking.BuiltinPatterns)
		require.Len(t, decoded.Masking.CustomPatterns, 1)
		require.NotNil(t, decoded.Masking.GlobalReplacement)
		assert.Equal(t, "[REDACTED]", *decoded.Masking.GlobalReplacement)
		assert.True(t, decoded.Masking.HidePatternType)
	})
}

func TestApplyMaskingToEntries(t *testing.T) {
	t.Run("nil masking config returns unchanged entries", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1234567890", Line: "user@example.com logged in", Labels: map[string]string{"app": "test"}},
		}

		result, err := applyMaskingToEntries(entries, nil)
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Equal(t, "user@example.com logged in", result[0].Line)
	})

	t.Run("empty masking config returns unchanged entries", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1234567890", Line: "user@example.com logged in", Labels: map[string]string{"app": "test"}},
		}

		result, err := applyMaskingToEntries(entries, &MaskingConfig{})
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Equal(t, "user@example.com logged in", result[0].Line)
	})

	t.Run("applies builtin email masking", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1234567890", Line: "User user@example.com logged in", Labels: map[string]string{"app": "test"}},
		}

		result, err := applyMaskingToEntries(entries, &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		})
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Equal(t, "User [MASKED:email] logged in", result[0].Line)
	})

	t.Run("applies multiple builtin patterns", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1234567890", Line: "User user@example.com from 192.168.1.100", Labels: map[string]string{"app": "test"}},
		}

		result, err := applyMaskingToEntries(entries, &MaskingConfig{
			BuiltinPatterns: []string{"email", "ip_address"},
		})
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Contains(t, result[0].Line, "[MASKED:email]")
		assert.Contains(t, result[0].Line, "[MASKED:ip_address]")
	})

	t.Run("applies custom pattern masking", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1234567890", Line: "Found secret-abc123 in request", Labels: map[string]string{"app": "test"}},
		}

		result, err := applyMaskingToEntries(entries, &MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: `secret-\w+`, Replacement: "[SECRET]"},
			},
		})
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Equal(t, "Found [SECRET] in request", result[0].Line)
	})

	t.Run("applies global replacement", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1234567890", Line: "user@example.com and +819012345678", Labels: map[string]string{"app": "test"}},
		}

		globalReplacement := "***"
		result, err := applyMaskingToEntries(entries, &MaskingConfig{
			BuiltinPatterns:   []string{"email", "phone"},
			GlobalReplacement: &globalReplacement,
		})
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Equal(t, "*** and ***", result[0].Line)
	})

	t.Run("handles empty entries slice", func(t *testing.T) {
		result, err := applyMaskingToEntries([]LogEntry{}, &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("masks multiple entries", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1", Line: "first@email.com"},
			{Timestamp: "2", Line: "no email here"},
			{Timestamp: "3", Line: "third@email.org"},
		}

		result, err := applyMaskingToEntries(entries, &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		})
		require.NoError(t, err)

		require.Len(t, result, 3)
		assert.Equal(t, "[MASKED:email]", result[0].Line)
		assert.Equal(t, "no email here", result[1].Line)
		assert.Equal(t, "[MASKED:email]", result[2].Line)
	})

	t.Run("preserves other entry fields during masking", func(t *testing.T) {
		entries := []LogEntry{
			{
				Timestamp: "2024-01-01T00:00:00Z",
				Line:      "user@example.com",
				Labels:    map[string]string{"app": "test", "env": "prod"},
			},
		}

		result, err := applyMaskingToEntries(entries, &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		})
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Equal(t, "2024-01-01T00:00:00Z", result[0].Timestamp)
		assert.Equal(t, "[MASKED:email]", result[0].Line)
		assert.Equal(t, map[string]string{"app": "test", "env": "prod"}, result[0].Labels)
	})

	t.Run("returns error for invalid builtin pattern", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1", Line: "test"},
		}

		_, err := applyMaskingToEntries(entries, &MaskingConfig{
			BuiltinPatterns: []string{"invalid_pattern"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid builtin pattern")
	})

	t.Run("returns error for invalid regex pattern", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1", Line: "test"},
		}

		_, err := applyMaskingToEntries(entries, &MaskingConfig{
			CustomPatterns: []MaskingPattern{
				{Pattern: `[invalid`},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid regex pattern")
	})

	t.Run("returns error for too many patterns", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: "1", Line: "test"},
		}

		// Create 21 patterns to exceed the limit
		patterns := make([]MaskingPattern, 21)
		for i := range patterns {
			patterns[i] = MaskingPattern{Pattern: fmt.Sprintf("pattern%d", i)}
		}

		_, err := applyMaskingToEntries(entries, &MaskingConfig{
			CustomPatterns: patterns,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many")
	})

	t.Run("metric entries are not masked (Value field)", func(t *testing.T) {
		v := 42.5
		entries := []LogEntry{
			{Timestamp: "1", Value: &v, Labels: map[string]string{"__type__": "metrics"}},
		}

		result, err := applyMaskingToEntries(entries, &MaskingConfig{
			BuiltinPatterns: []string{"email"},
		})
		require.NoError(t, err)

		require.Len(t, result, 1)
		assert.Equal(t, 42.5, *result[0].Value)
		assert.Empty(t, result[0].Line) // Line should still be empty
	})
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func generateTestLogEntries(n int) []LogEntry {
	entries := make([]LogEntry, n)
	// Create realistic log lines with various sensitive data patterns
	logTemplates := []string{
		"INFO User user%d@example.com logged in from 192.168.1.%d",
		"DEBUG Processing payment for card 4111-1111-1111-%04d",
		"WARN Failed login attempt for +8190%08d from IP 10.0.%d.%d",
		"ERROR API call failed: api_key=sk_live_abc%020d",
		"INFO Session token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIlZCJ9.sig%d",
		"DEBUG Device %02X:%02X:%02X:%02X:%02X:%02X connected",
		"INFO Request from 2001:0db8:85a3:0000:0000:8a2e:0370:%04x processed",
		"WARN User user%d@company.org attempted to access restricted resource",
		"ERROR Connection from 172.16.%d.%d failed: secret=abc%016d",
		"INFO Notification sent to +1415555%04d",
	}

	for i := 0; i < n; i++ {
		template := logTemplates[i%len(logTemplates)]
		var line string
		switch i % len(logTemplates) {
		case 0:
			line = fmt.Sprintf(template, i, i%256)
		case 1:
			line = fmt.Sprintf(template, i%10000)
		case 2:
			line = fmt.Sprintf(template, i, i%256, i%256)
		case 3:
			line = fmt.Sprintf(template, i)
		case 4:
			line = fmt.Sprintf(template, i, i)
		case 5:
			line = fmt.Sprintf(template, i%256, (i+1)%256, (i+2)%256, (i+3)%256, (i+4)%256, (i+5)%256)
		case 6:
			line = fmt.Sprintf(template, i%65536)
		case 7:
			line = fmt.Sprintf(template, i)
		case 8:
			line = fmt.Sprintf(template, i%256, i%256, i)
		case 9:
			line = fmt.Sprintf(template, i%10000)
		}

		entries[i] = LogEntry{
			Timestamp: fmt.Sprintf("2024-01-01T00:00:%02d.%09dZ", i%60, i),
			Line:      line,
			Labels:    map[string]string{"app": "benchmark", "instance": fmt.Sprintf("node-%d", i%10)},
		}
	}
	return entries
}

func BenchmarkLogMasker_MaskEntries_100Entries(b *testing.B) {
	// Setup: Create masker with all builtin patterns (7 patterns)
	config := &MaskingConfig{
		BuiltinPatterns: []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		},
	}
	masker, err := NewLogMasker(config)
	if err != nil {
		b.Fatalf("Failed to create masker: %v", err)
	}

	// Generate 100 test entries
	entries := generateTestLogEntries(100)

	// Reset timer to exclude setup time
	b.ResetTimer()

	// Run benchmark
	for i := 0; i < b.N; i++ {
		// Create a copy of entries for each iteration to avoid caching effects
		entriesCopy := make([]LogEntry, len(entries))
		copy(entriesCopy, entries)

		_ = masker.MaskEntries(entriesCopy)
	}
}

func BenchmarkLogMasker_MaskEntries_20Patterns(b *testing.B) {
	// Setup: Create masker with 7 builtin + 13 custom patterns = 20 total (max limit)
	config := &MaskingConfig{
		BuiltinPatterns: []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		},
		CustomPatterns: []MaskingPattern{
			{Pattern: `user_id=\d+`},
			{Pattern: `session_id=[a-f0-9]{32}`},
			{Pattern: `order_\d{8}`},
			{Pattern: `customer_[A-Z]{3}\d{6}`},
			{Pattern: `txn_[a-z0-9]{16}`},
			{Pattern: `ref_\d{10}`},
			{Pattern: `internal_[a-zA-Z0-9_]{8,32}`},
			{Pattern: `\bSSN:\s*\d{3}-\d{2}-\d{4}\b`},
			{Pattern: `\bDOB:\s*\d{4}-\d{2}-\d{2}\b`},
			{Pattern: `\baccount:\s*\d{10,16}\b`},
			{Pattern: `\brouting:\s*\d{9}\b`},
			{Pattern: `\bpin:\s*\d{4,6}\b`},
			{Pattern: `\bcvv:\s*\d{3,4}\b`},
		},
	}
	masker, err := NewLogMasker(config)
	if err != nil {
		b.Fatalf("Failed to create masker: %v", err)
	}

	// Generate 100 test entries
	entries := generateTestLogEntries(100)

	// Reset timer to exclude setup time
	b.ResetTimer()

	// Run benchmark
	for i := 0; i < b.N; i++ {
		// Create a copy of entries for each iteration
		entriesCopy := make([]LogEntry, len(entries))
		copy(entriesCopy, entries)

		_ = masker.MaskEntries(entriesCopy)
	}
}

func BenchmarkLogMasker_MaskEntries_SLO(b *testing.B) {
	// Setup: Create masker with 20 patterns (max limit)
	config := &MaskingConfig{
		BuiltinPatterns: []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		},
		CustomPatterns: []MaskingPattern{
			{Pattern: `user_id=\d+`},
			{Pattern: `session_id=[a-f0-9]{32}`},
			{Pattern: `order_\d{8}`},
			{Pattern: `customer_[A-Z]{3}\d{6}`},
			{Pattern: `txn_[a-z0-9]{16}`},
			{Pattern: `ref_\d{10}`},
			{Pattern: `internal_[a-zA-Z0-9_]{8,32}`},
			{Pattern: `\bSSN:\s*\d{3}-\d{2}-\d{4}\b`},
			{Pattern: `\bDOB:\s*\d{4}-\d{2}-\d{2}\b`},
			{Pattern: `\baccount:\s*\d{10,16}\b`},
			{Pattern: `\brouting:\s*\d{9}\b`},
			{Pattern: `\bpin:\s*\d{4,6}\b`},
			{Pattern: `\bcvv:\s*\d{3,4}\b`},
		},
	}
	masker, err := NewLogMasker(config)
	if err != nil {
		b.Fatalf("Failed to create masker: %v", err)
	}

	// Verify we have 20 patterns
	if masker.PatternCount() != 20 {
		b.Fatalf("Expected 20 patterns, got %d", masker.PatternCount())
	}

	// Generate 100 test entries
	entries := generateTestLogEntries(100)

	// Reset timer
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		entriesCopy := make([]LogEntry, len(entries))
		copy(entriesCopy, entries)
		_ = masker.MaskEntries(entriesCopy)
	}
}

func BenchmarkLogMasker_PatternCompilation(b *testing.B) {
	// Setup config with all patterns
	config := &MaskingConfig{
		BuiltinPatterns: []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		},
		CustomPatterns: []MaskingPattern{
			{Pattern: `user_id=\d+`},
			{Pattern: `session_id=[a-f0-9]{32}`},
			{Pattern: `order_\d{8}`},
			{Pattern: `customer_[A-Z]{3}\d{6}`},
			{Pattern: `txn_[a-z0-9]{16}`},
			{Pattern: `ref_\d{10}`},
			{Pattern: `internal_[a-zA-Z0-9_]{8,32}`},
			{Pattern: `\bSSN:\s*\d{3}-\d{2}-\d{4}\b`},
			{Pattern: `\bDOB:\s*\d{4}-\d{2}-\d{2}\b`},
			{Pattern: `\baccount:\s*\d{10,16}\b`},
			{Pattern: `\brouting:\s*\d{9}\b`},
			{Pattern: `\bpin:\s*\d{4,6}\b`},
			{Pattern: `\bcvv:\s*\d{3,4}\b`},
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = NewLogMasker(config)
	}
}

func BenchmarkLogMasker_MaskEntries_SingleEntry(b *testing.B) {
	config := &MaskingConfig{
		BuiltinPatterns: []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		},
	}
	masker, err := NewLogMasker(config)
	if err != nil {
		b.Fatalf("Failed to create masker: %v", err)
	}

	entry := LogEntry{
		Timestamp: "2024-01-01T00:00:00Z",
		Line:      "User user@example.com logged in from 192.168.1.100 with card 4111-1111-1111-1111",
		Labels:    map[string]string{"app": "test"},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		entryCopy := entry
		_ = masker.MaskEntries([]LogEntry{entryCopy})
	}
}

func BenchmarkLogMasker_MaskEntries_NoMatch(b *testing.B) {
	config := &MaskingConfig{
		BuiltinPatterns: []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		},
	}
	masker, err := NewLogMasker(config)
	if err != nil {
		b.Fatalf("Failed to create masker: %v", err)
	}

	// Create entries that won't match any pattern
	entries := make([]LogEntry, 100)
	for i := 0; i < 100; i++ {
		entries[i] = LogEntry{
			Timestamp: fmt.Sprintf("2024-01-01T00:00:%02dZ", i%60),
			Line:      fmt.Sprintf("INFO This is a regular log message number %d without sensitive data", i),
			Labels:    map[string]string{"app": "test"},
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		entriesCopy := make([]LogEntry, len(entries))
		copy(entriesCopy, entries)
		_ = masker.MaskEntries(entriesCopy)
	}
}

func TestSLOCompliance(t *testing.T) {
	// Setup: Create masker with 20 patterns (max limit)
	config := &MaskingConfig{
		BuiltinPatterns: []string{
			"email", "phone", "credit_card", "ip_address",
			"mac_address", "api_key", "jwt_token",
		},
		CustomPatterns: []MaskingPattern{
			{Pattern: `user_id=\d+`},
			{Pattern: `session_id=[a-f0-9]{32}`},
			{Pattern: `order_\d{8}`},
			{Pattern: `customer_[A-Z]{3}\d{6}`},
			{Pattern: `txn_[a-z0-9]{16}`},
			{Pattern: `ref_\d{10}`},
			{Pattern: `internal_[a-zA-Z0-9_]{8,32}`},
			{Pattern: `\bSSN:\s*\d{3}-\d{2}-\d{4}\b`},
			{Pattern: `\bDOB:\s*\d{4}-\d{2}-\d{2}\b`},
			{Pattern: `\baccount:\s*\d{10,16}\b`},
			{Pattern: `\brouting:\s*\d{9}\b`},
			{Pattern: `\bpin:\s*\d{4,6}\b`},
			{Pattern: `\bcvv:\s*\d{3,4}\b`},
		},
	}
	masker, err := NewLogMasker(config)
	require.NoError(t, err)

	// Verify we have 20 patterns
	require.Equal(t, 20, masker.PatternCount(), "Expected 20 patterns for SLO test")

	// Generate 100 test entries with realistic content
	entries := generateTestLogEntries(100)

	// Run multiple iterations and take the average to reduce variance
	const iterations = 10
	var totalDuration int64

	for i := 0; i < iterations; i++ {
		entriesCopy := make([]LogEntry, len(entries))
		copy(entriesCopy, entries)

		start := time.Now()
		_ = masker.MaskEntries(entriesCopy)
		duration := time.Since(start)
		totalDuration += duration.Nanoseconds()
	}

	averageDuration := time.Duration(totalDuration / iterations)
	sloLimit := 100 * time.Millisecond

	t.Logf("SLO Test Results:")
	t.Logf("  - Entries: 100")
	t.Logf("  - Patterns: %d", masker.PatternCount())
	t.Logf("  - Average duration: %v", averageDuration)
	t.Logf("  - SLO limit: %v", sloLimit)

	// Assert SLO compliance
	assert.Less(t, averageDuration, sloLimit,
		"SLO violation: masking 100 entries with 20 patterns took %v (limit: %v)",
		averageDuration, sloLimit)
}

func TestPatternCompilationOnce(t *testing.T) {
	config := &MaskingConfig{
		BuiltinPatterns: []string{"email", "phone"},
		CustomPatterns: []MaskingPattern{
			{Pattern: `custom-\d+`},
		},
	}

	// Create masker (compiles patterns)
	masker, err := NewLogMasker(config)
	require.NoError(t, err)
	require.NotNil(t, masker)

	// Verify pattern count
	assert.Equal(t, 3, masker.PatternCount())

	// Create test entries
	entries := []LogEntry{
		{Line: "user@example.com"},
		{Line: "+819012345678"},
		{Line: "custom-12345"},
	}

	// Time multiple calls to MaskEntries - each should be fast since patterns are pre-compiled
	const iterations = 1000
	start := time.Now()
	for i := 0; i < iterations; i++ {
		entriesCopy := make([]LogEntry, len(entries))
		copy(entriesCopy, entries)
		_ = masker.MaskEntries(entriesCopy)
	}
	totalDuration := time.Since(start)

	// Average time per call should be very low (microseconds, not milliseconds)
	// This confirms patterns are not being recompiled each time
	averagePerCall := totalDuration / iterations
	t.Logf("Average time per MaskEntries call: %v", averagePerCall)

	// If patterns were being recompiled each time, this would be much slower
	// Typical compilation time for 3 patterns is ~10-50µs, but masking pre-compiled is ~1-10µs
	assert.Less(t, averagePerCall, 1*time.Millisecond,
		"MaskEntries is too slow, patterns may be recompiling each call")
}
