package tools

import (
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
)

// parseStartTime parses start time strings in various formats.
// Supports: "now", "now-Xs/m/h/d/w", RFC3339, ISO dates, and Unix timestamps.
func parseStartTime(timeStr string) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, nil
	}

	tr := gtime.TimeRange{
		From: timeStr,
		Now:  time.Now(),
	}
	return tr.ParseFrom()
}

// parseEndTime parses end time strings in various formats.
// For end times, date-only strings resolve to end of day rather than start.
func parseEndTime(timeStr string) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, nil
	}

	tr := gtime.TimeRange{
		To:  timeStr,
		Now: time.Now(),
	}
	return tr.ParseTo()
}
