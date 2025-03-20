//go:build cloud
// +build cloud

package tools

import (
	"context"
	"os"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createOnCallCloudTestContext(t *testing.T) context.Context {
	grafanaURL := os.Getenv("GRAFANA_URL")
	if grafanaURL == "" {
		t.Skip("GRAFANA_URL environment variable not set, skipping cloud OnCall integration tests")
	}

	grafanaApiKey := os.Getenv("GRAFANA_API_KEY")
	if grafanaApiKey == "" {
		t.Skip("GRAFANA_API_KEY environment variable not set, skipping cloud OnCall integration tests")
	}

	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaURL(ctx, grafanaURL)
	ctx = mcpgrafana.WithGrafanaAPIKey(ctx, grafanaApiKey)

	return ctx
}

func TestCloudOnCallSchedules(t *testing.T) {
	ctx := createOnCallCloudTestContext(t)

	// Test listing all schedules
	t.Run("list all schedules", func(t *testing.T) {
		result, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{})
		require.NoError(t, err, "Should not error when listing schedules")

		// We can't assert exact counts or values since we're using a real instance,
		// but we can check that the call succeeded and returned some data
		assert.NotNil(t, result, "Result should not be nil")
	})

	// Test with a limit parameter
	t.Run("list schedules with limit", func(t *testing.T) {
		limit := 1
		result, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{
			Limit: limit,
		})
		require.NoError(t, err, "Should not error when listing schedules with limit")
		assert.LessOrEqual(t, len(result), limit,
			"Result count should respect the limit parameter")
	})
}

func TestCloudOnCallShift(t *testing.T) {
	ctx := createOnCallCloudTestContext(t)

	// First get a schedule to find a valid shift
	schedules, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{
		Limit: 1,
	})
	require.NoError(t, err, "Should not error when listing schedules")
	require.NotEmpty(t, schedules, "Should have at least one schedule to test with")
	require.NotNil(t, schedules[0].Shifts, "Schedule should have shifts field")
	require.NotEmpty(t, *schedules[0].Shifts, "Schedule should have at least one shift")

	shifts := *schedules[0].Shifts
	shiftID := shifts[0]

	// Test getting shift details with valid ID
	t.Run("get shift details", func(t *testing.T) {
		result, err := getOnCallShift(ctx, GetOnCallShiftParams{
			ShiftID: shiftID,
		})
		require.NoError(t, err, "Should not error when getting shift details")
		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, shiftID, result.ID, "Should return the correct shift")
	})

	t.Run("get shift with invalid ID", func(t *testing.T) {
		_, err := getOnCallShift(ctx, GetOnCallShiftParams{
			ShiftID: "invalid-shift-id",
		})
		assert.Error(t, err, "Should error when getting shift with invalid ID")
	})
}
