package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClickHouseHelperFunctions(t *testing.T) {
	t.Run("toString", func(t *testing.T) {
		tests := []struct {
			input    interface{}
			expected string
		}{
			{"hello", "hello"},
			{123, "123"},
			{int64(456), "456"},
			{float64(123.45), "123.45"},
			{true, "true"},
			{nil, ""},
		}

		for _, test := range tests {
			assert.Equal(t, test.expected, toString(test.input), "toString(%v)", test.input)
		}
	})

	t.Run("toUint64", func(t *testing.T) {
		tests := []struct {
			input    interface{}
			expected uint64
		}{
			{123, uint64(123)},
			{int64(456), uint64(456)},
			{float64(789.0), uint64(789)},
			{float64(789.9), uint64(789)}, // Should truncate
			{uint64(999), uint64(999)},
			{"123", uint64(123)},
			{"456.78", uint64(0)}, // Invalid string should return 0
			{"invalid", uint64(0)},
			{nil, uint64(0)},
			{true, uint64(0)}, // Unsupported type should return 0
		}

		for _, test := range tests {
			assert.Equal(t, test.expected, toUint64(test.input), "toUint64(%v)", test.input)
		}
	})

	t.Run("enforceClickHouseLimit", func(t *testing.T) {
		tests := []struct {
			input    int
			expected int
		}{
			{0, DefaultClickHouseLimit},
			{-1, DefaultClickHouseLimit},
			{50, 50},
			{DefaultClickHouseLimit, DefaultClickHouseLimit},
			{MaxClickHouseLimit, MaxClickHouseLimit},
			{MaxClickHouseLimit + 100, MaxClickHouseLimit}, // Should be capped
			{2000, MaxClickHouseLimit},                     // Should be capped
		}

		for _, test := range tests {
			assert.Equal(t, test.expected, enforceClickHouseLimit(test.input), "enforceClickHouseLimit(%d)", test.input)
		}
	})
}

func TestClickHouseConstants(t *testing.T) {
	t.Run("constants are reasonable", func(t *testing.T) {
		assert.Greater(t, DefaultClickHouseLimit, 0, "Default limit should be positive")
		assert.Greater(t, MaxClickHouseLimit, DefaultClickHouseLimit, "Max limit should be greater than default")
		assert.Equal(t, 100, DefaultClickHouseLimit, "Default limit should be 100")
		assert.Equal(t, 1000, MaxClickHouseLimit, "Max limit should be 1000")
	})
}
