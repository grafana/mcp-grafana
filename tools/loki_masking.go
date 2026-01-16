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
type MaskingConfig struct {
	BuiltinPatterns   []string         `json:"builtinPatterns,omitempty" jsonschema:"description=List of builtin pattern identifiers to apply (email\\, phone\\, credit_card\\, ip_address\\, mac_address\\, api_key\\, jwt_token)"`
	CustomPatterns    []MaskingPattern `json:"customPatterns,omitempty" jsonschema:"description=List of custom regex patterns to apply"`
	GlobalReplacement *string          `json:"globalReplacement,omitempty" jsonschema:"description=Custom replacement string for all patterns. Overrides pattern-specific defaults. Empty string removes matched content."`
	HidePatternType   bool             `json:"hidePatternType,omitempty" jsonschema:"description=When true\\, uses generic [MASKED] instead of [MASKED:type]"`
}

// MaskingPattern defines a custom masking pattern
type MaskingPattern struct {
	Pattern     string `json:"pattern" jsonschema:"required,description=RE2 regular expression pattern to match"`
	Replacement string `json:"replacement,omitempty" jsonschema:"description=Custom replacement string. Defaults to [MASKED:custom] if empty. Back-references not supported."`
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

	totalPatterns := len(config.BuiltinPatterns) + len(config.CustomPatterns)
	if totalPatterns > MaxMaskingPatterns {
		return fmt.Errorf("%w: got %d patterns, maximum is %d",
			ErrTooManyPatterns, totalPatterns, MaxMaskingPatterns)
	}

	for _, id := range config.BuiltinPatterns {
		if !IsValidBuiltinPattern(id) {
			return fmt.Errorf("%w: %q (available: %v)",
				ErrInvalidBuiltinPattern, id, validBuiltinPatterns)
		}
	}

	for _, pattern := range config.CustomPatterns {
		_, err := regexp.Compile(pattern.Pattern)
		if err != nil {
			return fmt.Errorf("%w: pattern %q: %v",
				ErrInvalidRegexPattern, pattern.Pattern, err)
		}
	}

	return nil
}

type compiledPattern struct {
	regex       *regexp.Regexp
	replacement string
}

// LogMasker provides log masking functionality
type LogMasker struct {
	patterns          []*compiledPattern
	globalReplacement *string
	hideType          bool
}

func NewLogMasker(config *MaskingConfig) (*LogMasker, error) {
	if config == nil {
		return nil, nil
	}

	if err := ValidateMaskingConfig(config); err != nil {
		return nil, err
	}

	masker := &LogMasker{
		patterns:          make([]*compiledPattern, 0, len(config.BuiltinPatterns)+len(config.CustomPatterns)),
		globalReplacement: config.GlobalReplacement,
		hideType:          config.HidePatternType,
	}

	for _, id := range config.BuiltinPatterns {
		regex, err := GetBuiltinPattern(id)
		if err != nil {
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

	for _, pattern := range config.CustomPatterns {
		regex, err := regexp.Compile(pattern.Pattern)
		if err != nil {
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

func (m *LogMasker) PatternCount() int {
	if m == nil {
		return 0
	}
	return len(m.patterns)
}

func (m *LogMasker) HasGlobalReplacement() bool {
	if m == nil {
		return false
	}
	return m.globalReplacement != nil
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
		replacement := pattern.replacement
		if m.globalReplacement != nil {
			replacement = *m.globalReplacement
		}
		line = pattern.regex.ReplaceAllLiteralString(line, replacement)
	}

	return line
}
