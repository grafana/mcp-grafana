package tools

import (
	"fmt"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
)

// timeParamHint returns the canonical phrase appended to time-parameter parse
// errors. It warns callers that naive timestamps (no timezone offset) are
// interpreted as UTC by the server, and suggests including an explicit offset
// like '-05:00' or using the relative 'now-Xh' syntax accepted by gtime.
func timeParamHint() string {
	return "Timestamps without a timezone offset are interpreted as UTC; include an offset like '-05:00' or use relative syntax like 'now-1h'"
}

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
	t, err := tr.ParseFrom()
	if err != nil {
		return time.Time{}, fmt.Errorf("%w. %s", err, timeParamHint())
	}
	return t, nil
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
	t, err := tr.ParseTo()
	if err != nil {
		return time.Time{}, fmt.Errorf("%w. %s", err, timeParamHint())
	}
	return t, nil
}

// parseTimeRange resolves start , end times to valid time.Time objects
// defaults to 1hour period for missing start/end value
// Supports: "now", "now-Xs/m/h/d/w", RFC3339, ISO dates, and Unix timestamps.
func parseTimeRange(start string, end string) (*time.Time, *time.Time, error) {
	// Parse time range
	defaultPeriod := time.Hour

	now := time.Now()
	fromTime := now.Add(-1 * defaultPeriod) // Default: 1 hour ago
	toTime := now                           // Default: now

	if start != "" {
		parsed, err := parseStartTime(start)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}

		// set relative end time 1hour from start
		if end == "" {
			toTime = fromTime.Add(defaultPeriod)
		}
	}

	if end != "" {
		parsed, err := parseEndTime(end)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}

		if start == "" {
			fromTime = toTime.Add(-1 * defaultPeriod)
		}
	}

	return &fromTime, &toTime, nil

}
