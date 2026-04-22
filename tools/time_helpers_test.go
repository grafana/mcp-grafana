//go:build unit

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStartTime(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		checkFunc   func(t *testing.T, result time.Time)
	}{
		{
			name:  "empty string returns zero time",
			input: "",
			checkFunc: func(t *testing.T, result time.Time) {
				assert.True(t, result.IsZero())
			},
		},
		{
			name:  "now returns current time",
			input: "now",
			checkFunc: func(t *testing.T, result time.Time) {
				assert.WithinDuration(t, time.Now(), result, 5*time.Second)
			},
		},
		{
			name:  "now-1h returns time 1 hour ago",
			input: "now-1h",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Now().Add(-1 * time.Hour)
				assert.WithinDuration(t, expected, result, 5*time.Second)
			},
		},
		{
			name:  "now-30m returns time 30 minutes ago",
			input: "now-30m",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Now().Add(-30 * time.Minute)
				assert.WithinDuration(t, expected, result, 5*time.Second)
			},
		},
		{
			name:  "now-6h returns time 6 hours ago",
			input: "now-6h",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Now().Add(-6 * time.Hour)
				assert.WithinDuration(t, expected, result, 5*time.Second)
			},
		},
		{
			name:  "now-1d returns time 1 day ago",
			input: "now-1d",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Now().Add(-24 * time.Hour)
				assert.WithinDuration(t, expected, result, 5*time.Second)
			},
		},
		{
			name:  "RFC3339 format",
			input: "2024-01-15T10:00:00Z",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
				assert.Equal(t, expected, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseStartTime(tt.input)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestParseEndTime(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		checkFunc   func(t *testing.T, result time.Time)
	}{
		{
			name:  "empty string returns zero time",
			input: "",
			checkFunc: func(t *testing.T, result time.Time) {
				assert.True(t, result.IsZero())
			},
		},
		{
			name:  "now returns current time",
			input: "now",
			checkFunc: func(t *testing.T, result time.Time) {
				assert.WithinDuration(t, time.Now(), result, 5*time.Second)
			},
		},
		{
			name:  "now-1h returns time 1 hour ago",
			input: "now-1h",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Now().Add(-1 * time.Hour)
				assert.WithinDuration(t, expected, result, 5*time.Second)
			},
		},
		{
			name:  "RFC3339 format",
			input: "2024-01-15T10:00:00Z",
			checkFunc: func(t *testing.T, result time.Time) {
				expected := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
				assert.Equal(t, expected, result)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseEndTime(tt.input)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}
