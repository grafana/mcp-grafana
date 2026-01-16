package tools

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
