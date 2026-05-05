package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIndexMatchesPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		index   string
		want    bool
	}{
		{name: "empty pattern matches anything", pattern: "", index: "logs-2024.01.01", want: true},
		{name: "exact match", pattern: "logs-*", index: "logs-*", want: true},
		{name: "wildcard matches specific index", pattern: "logs-*", index: "logs-2024.01.01", want: true},
		{name: "wildcard matches sub-pattern", pattern: "logs-*", index: "logs-2024*", want: true},
		{name: "incompatible index rejected", pattern: "logs-*", index: "metrics-2024.01.01", want: false},
		{name: "incompatible pattern rejected", pattern: "logs-*", index: "metrics-*", want: false},
		{name: "specific pattern matches exact", pattern: "logs-2024.01.01", index: "logs-2024.01.01", want: true},
		{name: "specific pattern rejects different index", pattern: "logs-2024.01.01", index: "logs-2024.01.02", want: false},
		{name: "star matches everything", pattern: "*", index: "anything", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := indexMatchesPattern(tt.pattern, tt.index)
			assert.Equal(t, tt.want, got)
		})
	}
}
