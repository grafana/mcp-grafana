//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The interval macros and auto interval variables match Grafana's calculateInterval. The
// fixtures come from running Grafana's own code; see testdata/interval/README.md.
func TestIntervalMatchesGrafana(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "interval", "golden.json"))
	require.NoError(t, err)
	var golden struct {
		Cases []struct {
			SpanMs     int64   `json:"span_ms"`
			Steps      float64 `json:"steps"`
			Min        string  `json:"min"`
			Interval   string  `json:"interval"`
			IntervalMs int64   `json:"interval_ms"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &golden))
	require.Len(t, golden.Cases, 515)

	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	for _, c := range golden.Cases {
		span := time.Duration(c.SpanMs) * time.Millisecond
		if c.Min != "" {
			assert.Equal(t, c.Interval, grafanaCalculateInterval(span, c.Steps, c.Min), "span %dms, %v steps, min %s", c.SpanMs, c.Steps, c.Min)
		}
		want := fmt.Sprintf("%s %d %s", c.Interval, c.IntervalMs, c.Interval)
		got := substituteGrafanaMacros("$__interval ${__interval_ms} $__auto", start, start.Add(span), intervalOptions{maxDataPoints: c.Steps, minInterval: c.Min})
		assert.Equal(t, want, got, "span %dms, %v steps, min %q", c.SpanMs, c.Steps, c.Min)
	}
}
