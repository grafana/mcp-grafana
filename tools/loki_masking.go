package tools

import (
	"errors"
	"fmt"
	"regexp"
)

// BuiltinPatternID represents a builtin pattern identifier
type BuiltinPatternID string

const (
	PatternEmail      BuiltinPatternID = "email"
	PatternPhone      BuiltinPatternID = "phone"
	PatternCreditCard BuiltinPatternID = "credit_card"
	PatternIPAddress  BuiltinPatternID = "ip_address"
	PatternMACAddress BuiltinPatternID = "mac_address"
	PatternAPIKey     BuiltinPatternID = "api_key"
	PatternJWTToken   BuiltinPatternID = "jwt_token"
)

var builtinPatterns = map[BuiltinPatternID]*regexp.Regexp{
	PatternEmail:      regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),
	PatternPhone:      regexp.MustCompile(`\+[1-9]\d{6,14}`),
	PatternCreditCard: regexp.MustCompile(`\b(?:\d{4}[-\s]?){3}\d{4}\b`),
	PatternIPAddress:  regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b`),
	PatternMACAddress: regexp.MustCompile(`\b(?:[0-9a-fA-F]{2}[:-]){5}[0-9a-fA-F]{2}\b`),
	PatternAPIKey:     regexp.MustCompile(`(?i)(?:api[_-]?key|apikey|token|secret|password|auth)[=:\s]["']?[a-zA-Z0-9_\-]{16,}`),
	PatternJWTToken:   regexp.MustCompile(`eyJ[a-zA-Z0-9_-]*\.eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*`),
}

var validBuiltinPatterns = []BuiltinPatternID{
	PatternEmail,
	PatternPhone,
	PatternCreditCard,
	PatternIPAddress,
	PatternMACAddress,
	PatternAPIKey,
	PatternJWTToken,
}

func GetBuiltinPattern(id string) (*regexp.Regexp, error) {
	patternID := BuiltinPatternID(id)
	regex, exists := builtinPatterns[patternID]
	if !exists {
		return nil, fmt.Errorf("invalid builtin pattern identifier: %s (available: %v)", id, validBuiltinPatterns)
	}
	return regex, nil
}

func IsValidBuiltinPattern(id string) bool {
	patternID := BuiltinPatternID(id)
	_, exists := builtinPatterns[patternID]
	return exists
}

// MaskingConfig defines the masking configuration for log queries
// This is a simplified configuration that only supports builtin patterns.
// Custom patterns are not supported for security and simplicity reasons.
type MaskingConfig struct {
	BuiltinPatterns []string `json:"builtinPatterns,omitempty" jsonschema:"description=List of builtin pattern identifiers to apply (email\\, phone\\, credit_card\\, ip_address\\, mac_address\\, api_key\\, jwt_token)"`
}

const MaxMaskingPatterns = 20

var (
	ErrInvalidBuiltinPattern = errors.New("invalid builtin pattern identifier")
	ErrInvalidRegexPattern   = errors.New("invalid regex pattern")
	ErrTooManyPatterns       = errors.New("too many masking patterns")
	ErrMaskingFailed         = errors.New("masking operation failed: internal error")
)

func ValidateMaskingConfig(config *MaskingConfig) error {
	if config == nil {
		return nil
	}

	if len(config.BuiltinPatterns) > MaxMaskingPatterns {
		return fmt.Errorf("%w: got %d patterns, maximum is %d",
			ErrTooManyPatterns, len(config.BuiltinPatterns), MaxMaskingPatterns)
	}

	for _, id := range config.BuiltinPatterns {
		if !IsValidBuiltinPattern(id) {
			return fmt.Errorf("%w: %q (available: %v)",
				ErrInvalidBuiltinPattern, id, validBuiltinPatterns)
		}
	}

	return nil
}

type compiledPattern struct {
	regex       *regexp.Regexp
	replacement string
}

// LogMasker provides log masking functionality
// Patterns are precompiled at creation time for performance.
type LogMasker struct {
	patterns []*compiledPattern
}

func NewLogMasker(config *MaskingConfig) (*LogMasker, error) {
	if config == nil {
		return nil, nil
	}

	if err := ValidateMaskingConfig(config); err != nil {
		return nil, err
	}

	masker := &LogMasker{
		patterns: make([]*compiledPattern, 0, len(config.BuiltinPatterns)),
	}

	for _, id := range config.BuiltinPatterns {
		regex, err := GetBuiltinPattern(id)
		if err != nil {
			return nil, err
		}

		// Fixed replacement format: [MASKED:<pattern_id>]
		replacement := fmt.Sprintf("[MASKED:%s]", id)

		masker.patterns = append(masker.patterns, &compiledPattern{
			regex:       regex,
			replacement: replacement,
		})
	}

	return masker, nil
}

func (m *LogMasker) PatternCount() int {
	if m == nil {
		return 0
	}
	return len(m.patterns)
}

func (m *LogMasker) MaskEntries(entries []LogEntry) []LogEntry {
	if m == nil || len(m.patterns) == 0 {
		return entries
	}

	for i := range entries {
		entries[i].Line = m.maskLine(entries[i].Line)
	}

	return entries
}

func (m *LogMasker) maskLine(line string) string {
	if line == "" {
		return line
	}

	for _, pattern := range m.patterns {
		line = pattern.regex.ReplaceAllLiteralString(line, pattern.replacement)
	}

	return line
}
