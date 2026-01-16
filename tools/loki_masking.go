package tools

import (
	"errors"
	"fmt"
	"regexp"
)

// BuiltinPatternID represents a builtin pattern identifier.
// These identifiers can be used in MaskingConfig.BuiltinPatterns
// to apply predefined regex patterns for common sensitive data types.
type BuiltinPatternID string

// Builtin pattern identifier constants.
// Use these with MaskingConfig.BuiltinPatterns to enable masking for common data types.
const (
	// PatternEmail matches email addresses (e.g., user@example.com)
	PatternEmail BuiltinPatternID = "email"

	// PatternPhone matches E.164 international phone numbers (e.g., +819012345678)
	// Note: Local formats like 090-1234-5678 are NOT supported. Use custom patterns for local formats.
	PatternPhone BuiltinPatternID = "phone"

	// PatternCreditCard matches credit card numbers with optional separators (e.g., 4111-1111-1111-1111)
	PatternCreditCard BuiltinPatternID = "credit_card"

	// PatternIPAddress matches IPv4 addresses (e.g., 192.168.1.1) and full IPv6 addresses
	PatternIPAddress BuiltinPatternID = "ip_address"

	// PatternMACAddress matches MAC addresses with colon or dash separators (e.g., 00:1A:2B:3C:4D:5E)
	PatternMACAddress BuiltinPatternID = "mac_address"

	// PatternAPIKey matches common API key/secret patterns (e.g., api_key=xxx, token:xxx)
	PatternAPIKey BuiltinPatternID = "api_key"

	// PatternJWTToken matches JWT tokens (e.g., eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature)
	PatternJWTToken BuiltinPatternID = "jwt_token"
)

// builtinPatterns holds precompiled regex patterns for builtin identifiers.
// These patterns are compiled at package initialization time for performance.
var builtinPatterns = map[BuiltinPatternID]*regexp.Regexp{
	// Email: Standard email format
	PatternEmail: regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),

	// Phone: E.164 international format only (+から始まる7-15桁)
	// Local formats (090-xxxx-xxxx等) are not supported - use custom patterns if needed
	PatternPhone: regexp.MustCompile(`\+[1-9]\d{6,14}`),

	// Credit Card: 16 digits with optional dashes or spaces as separators
	PatternCreditCard: regexp.MustCompile(`\b(?:\d{4}[-\s]?){3}\d{4}\b`),

	// IP Address: IPv4 and full IPv6 addresses
	PatternIPAddress: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b`),

	// MAC Address: 6 groups of 2 hex digits separated by colons or dashes
	PatternMACAddress: regexp.MustCompile(`\b(?:[0-9a-fA-F]{2}[:-]){5}[0-9a-fA-F]{2}\b`),

	// API Key: Common patterns for API keys, tokens, secrets, passwords, and auth tokens
	// Matches key=value or key:value patterns where key contains common sensitive identifiers
	// and value is at least 16 characters of alphanumeric/underscore/dash
	PatternAPIKey: regexp.MustCompile(`(?i)(?:api[_-]?key|apikey|token|secret|password|auth)[=:\s]["']?[a-zA-Z0-9_\-]{16,}`),

	// JWT Token: Standard JWT format with three base64url-encoded parts separated by dots
	// All parts must start with "eyJ" (base64 encoding of '{"')
	PatternJWTToken: regexp.MustCompile(`eyJ[a-zA-Z0-9_-]*\.eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*`),
}

// validBuiltinPatterns is the list of valid builtin pattern identifiers.
// This slice is used for validation and error messages.
var validBuiltinPatterns = []BuiltinPatternID{
	PatternEmail,
	PatternPhone,
	PatternCreditCard,
	PatternIPAddress,
	PatternMACAddress,
	PatternAPIKey,
	PatternJWTToken,
}

// GetBuiltinPattern returns the compiled regex for the given builtin pattern identifier.
// Returns an error if the pattern identifier is not valid.
func GetBuiltinPattern(id string) (*regexp.Regexp, error) {
	patternID := BuiltinPatternID(id)
	regex, exists := builtinPatterns[patternID]
	if !exists {
		return nil, fmt.Errorf("invalid builtin pattern identifier: %s (available: %v)", id, validBuiltinPatterns)
	}
	return regex, nil
}

// IsValidBuiltinPattern checks if the given string is a valid builtin pattern identifier.
func IsValidBuiltinPattern(id string) bool {
	patternID := BuiltinPatternID(id)
	_, exists := builtinPatterns[patternID]
	return exists
}

// MaskingConfig defines the masking configuration for log queries.
// When applied, sensitive data in log lines will be replaced with mask strings
// before being returned to the client.
type MaskingConfig struct {
	// BuiltinPatterns is a list of builtin pattern identifiers to apply.
	// Available identifiers: email, phone, credit_card, ip_address, mac_address, api_key, jwt_token.
	// Note: "phone" pattern only matches E.164 international format (e.g., +819012345678).
	// For local formats, use custom patterns.
	BuiltinPatterns []string `json:"builtinPatterns,omitempty" jsonschema:"description=List of builtin pattern identifiers to apply (email\\, phone\\, credit_card\\, ip_address\\, mac_address\\, api_key\\, jwt_token)"`

	// CustomPatterns is a list of custom regex patterns with optional replacement strings.
	// Patterns use RE2 syntax (Go's regexp package).
	CustomPatterns []MaskingPattern `json:"customPatterns,omitempty" jsonschema:"description=List of custom regex patterns to apply"`

	// GlobalReplacement is a custom replacement string that overrides all pattern-specific replacements.
	// If nil, pattern-specific defaults are used ([MASKED:type] for builtin, [MASKED:custom] or custom replacement for custom patterns).
	// If set to empty string (""), matched content is deleted (removed from output).
	GlobalReplacement *string `json:"globalReplacement,omitempty" jsonschema:"description=Custom replacement string for all patterns. Overrides pattern-specific defaults. Empty string removes matched content."`

	// HidePatternType disables pattern type indication in mask output.
	// When true and GlobalReplacement is not set, uses generic [MASKED] instead of [MASKED:type].
	HidePatternType bool `json:"hidePatternType,omitempty" jsonschema:"description=When true\\, uses generic [MASKED] instead of [MASKED:type]"`
}

// MaskingPattern defines a custom masking pattern with an optional replacement string.
// The pattern uses RE2 regex syntax (Go's regexp package).
type MaskingPattern struct {
	// Pattern is the RE2 regex pattern to match.
	// Capture groups are allowed but back-references ($1, $2, etc.) in replacement are NOT supported.
	// The entire match is always replaced.
	Pattern string `json:"pattern" jsonschema:"required,description=RE2 regular expression pattern to match"`

	// Replacement is the optional custom replacement string.
	// If empty, defaults to [MASKED:custom].
	// Back-references ($1, $2, etc.) are NOT supported - the entire match is replaced with this literal string.
	Replacement string `json:"replacement,omitempty" jsonschema:"description=Custom replacement string. Defaults to [MASKED:custom] if empty. Back-references not supported."`
}

// MaxMaskingPatterns is the maximum number of masking patterns allowed (builtin + custom combined).
const MaxMaskingPatterns = 20

// Error types for masking operations.
var (
	// ErrInvalidBuiltinPattern indicates an unknown builtin pattern identifier was specified.
	ErrInvalidBuiltinPattern = errors.New("invalid builtin pattern identifier")

	// ErrInvalidRegexPattern indicates a custom pattern has invalid regex syntax.
	ErrInvalidRegexPattern = errors.New("invalid regex pattern")

	// ErrTooManyPatterns indicates the total number of patterns exceeds the limit.
	ErrTooManyPatterns = errors.New("too many masking patterns")

	// ErrMaskingFailed indicates an internal error occurred during masking operation.
	ErrMaskingFailed = errors.New("masking operation failed: internal error")
)

// ValidateMaskingConfig validates the masking configuration.
// Returns nil if config is nil or valid.
// Returns error with details if validation fails.
func ValidateMaskingConfig(config *MaskingConfig) error {
	// nil config means no masking - this is valid
	if config == nil {
		return nil
	}

	// Check total pattern count (builtin + custom)
	totalPatterns := len(config.BuiltinPatterns) + len(config.CustomPatterns)
	if totalPatterns > MaxMaskingPatterns {
		return fmt.Errorf("%w: got %d patterns, maximum is %d",
			ErrTooManyPatterns, totalPatterns, MaxMaskingPatterns)
	}

	// Validate builtin pattern identifiers
	for _, id := range config.BuiltinPatterns {
		if !IsValidBuiltinPattern(id) {
			return fmt.Errorf("%w: %q (available: %v)",
				ErrInvalidBuiltinPattern, id, validBuiltinPatterns)
		}
	}

	// Validate custom regex patterns
	for _, pattern := range config.CustomPatterns {
		_, err := regexp.Compile(pattern.Pattern)
		if err != nil {
			return fmt.Errorf("%w: pattern %q: %v",
				ErrInvalidRegexPattern, pattern.Pattern, err)
		}
	}

	return nil
}

// compiledPattern holds a compiled regex pattern with its replacement string.
type compiledPattern struct {
	regex       *regexp.Regexp
	replacement string // pattern-specific replacement (used if globalReplacement is nil)
}

// LogMasker provides log masking functionality.
// It holds pre-compiled patterns and applies them to log entries.
type LogMasker struct {
	patterns          []*compiledPattern
	globalReplacement *string // nil means use pattern-specific replacements
	hideType          bool
}

// NewLogMasker creates a new LogMasker from MaskingConfig.
// Returns nil if config is nil (no masking needed).
// Returns error if config validation fails or pattern compilation fails.
func NewLogMasker(config *MaskingConfig) (*LogMasker, error) {
	// nil config means no masking
	if config == nil {
		return nil, nil
	}

	// Validate configuration first
	if err := ValidateMaskingConfig(config); err != nil {
		return nil, err
	}

	masker := &LogMasker{
		patterns:          make([]*compiledPattern, 0, len(config.BuiltinPatterns)+len(config.CustomPatterns)),
		globalReplacement: config.GlobalReplacement,
		hideType:          config.HidePatternType,
	}

	// Add builtin patterns first (in order)
	for _, id := range config.BuiltinPatterns {
		regex, err := GetBuiltinPattern(id)
		if err != nil {
			// This shouldn't happen since we validated, but be defensive
			return nil, err
		}

		replacement := fmt.Sprintf("[MASKED:%s]", id)
		if config.HidePatternType {
			replacement = "[MASKED]"
		}

		masker.patterns = append(masker.patterns, &compiledPattern{
			regex:       regex,
			replacement: replacement,
		})
	}

	// Add custom patterns (in order)
	for _, pattern := range config.CustomPatterns {
		regex, err := regexp.Compile(pattern.Pattern)
		if err != nil {
			// This shouldn't happen since we validated, but be defensive
			return nil, fmt.Errorf("%w: pattern %q: %v",
				ErrInvalidRegexPattern, pattern.Pattern, err)
		}

		replacement := pattern.Replacement
		if replacement == "" {
			replacement = "[MASKED:custom]"
			if config.HidePatternType {
				replacement = "[MASKED]"
			}
		}

		masker.patterns = append(masker.patterns, &compiledPattern{
			regex:       regex,
			replacement: replacement,
		})
	}

	return masker, nil
}

// PatternCount returns the number of compiled patterns.
func (m *LogMasker) PatternCount() int {
	if m == nil {
		return 0
	}
	return len(m.patterns)
}

// HasGlobalReplacement returns true if a global replacement string is configured.
func (m *LogMasker) HasGlobalReplacement() bool {
	if m == nil {
		return false
	}
	return m.globalReplacement != nil
}

// MaskEntries applies masking to all log entries.
// Modifies entries in place and returns the same slice.
// If masker is nil or has no patterns, entries are returned unchanged.
func (m *LogMasker) MaskEntries(entries []LogEntry) []LogEntry {
	// nil masker means no masking
	if m == nil || len(m.patterns) == 0 {
		return entries
	}

	for i := range entries {
		entries[i].Line = m.maskLine(entries[i].Line)
	}

	return entries
}

// maskLine applies all patterns to a single line.
func (m *LogMasker) maskLine(line string) string {
	if line == "" {
		return line
	}

	for _, pattern := range m.patterns {
		replacement := pattern.replacement
		// Global replacement overrides pattern-specific replacement
		if m.globalReplacement != nil {
			replacement = *m.globalReplacement
		}

		// Use ReplaceAllLiteralString to disable back-references
		// This ensures $1, $2, etc. in replacement are treated as literal strings
		line = pattern.regex.ReplaceAllLiteralString(line, replacement)
	}

	return line
}
