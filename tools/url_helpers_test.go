package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildURL(t *testing.T) {
	const base = "http://proxy/api/datasources/proxy/uid"

	tests := []struct {
		name     string
		baseURL  string
		urlPath  string
		expected string
	}{
		{
			name:     "path with leading slash",
			baseURL:  base,
			urlPath:  "/_msearch",
			expected: base + "/_msearch",
		},
		{
			name:     "path without leading slash",
			baseURL:  base,
			urlPath:  "indexes",
			expected: base + "/indexes",
		},
		{
			name:     "base with trailing slash, path without leading slash",
			baseURL:  base + "/",
			urlPath:  "indexes",
			expected: base + "/indexes",
		},
		{
			name:     "base with trailing slash, path with leading slash",
			baseURL:  base + "/",
			urlPath:  "/indexes",
			expected: base + "/indexes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, buildURL(tt.baseURL, tt.urlPath))
		})
	}
}
