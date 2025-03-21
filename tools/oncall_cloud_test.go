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

	// Get a team ID from an existing schedule to test filtering
	schedules, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{})
	require.NoError(t, err, "Should not error when listing schedules")

	if len(schedules) > 0 && schedules[0].TeamId != "" {
		teamID := schedules[0].TeamId

		// Test filtering by team ID
		t.Run("list schedules by team ID", func(t *testing.T) {
			result, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{
				TeamID: teamID,
			})
			require.NoError(t, err, "Should not error when listing schedules by team")
			assert.NotEmpty(t, result, "Should return at least one schedule")
			for _, schedule := range result {
				assert.Equal(t, teamID, schedule.TeamId, "All schedules should belong to the specified team")
			}
		})
	}
}

func TestCloudOnCallShift(t *testing.T) {
	ctx := createOnCallCloudTestContext(t)

	// First get a schedule to find a valid shift
	schedules, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{})
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

func TestCloudGetCurrentOnCallUsers(t *testing.T) {
	ctx := createOnCallCloudTestContext(t)

	// First get a schedule to use for testing
	schedules, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{})
	require.NoError(t, err, "Should not error when listing schedules")
	require.NotEmpty(t, schedules, "Should have at least one schedule to test with")

	scheduleID := schedules[0].ID

	// Test getting current on-call users
	t.Run("get current on-call users", func(t *testing.T) {
		result, err := getCurrentOnCallUsers(ctx, GetCurrentOnCallUsersParams{
			ScheduleID: scheduleID,
		})
		require.NoError(t, err, "Should not error when getting current on-call users")
		assert.NotNil(t, result, "Result should not be nil")
		assert.Equal(t, scheduleID, result.ID, "Should return the correct schedule")
		// Note: We can't assert on OnCallNow contents as it depends on the actual schedule state
		assert.NotNil(t, result.OnCallNow, "OnCallNow field should be present")
	})

	t.Run("get current on-call users with invalid schedule ID", func(t *testing.T) {
		_, err := getCurrentOnCallUsers(ctx, GetCurrentOnCallUsersParams{
			ScheduleID: "invalid-schedule-id",
		})
		assert.Error(t, err, "Should error when getting current on-call users with invalid schedule ID")
	})
}

func TestCloudOnCallTeams(t *testing.T) {
	ctx := createOnCallCloudTestContext(t)

	t.Run("list teams", func(t *testing.T) {
		result, err := listOnCallTeams(ctx, ListOnCallTeamsParams{})
		require.NoError(t, err, "Should not error when listing teams")

		// We can't assert exact counts since we're using a real instance,
		// but we can check that the call succeeded and returned data
		assert.NotNil(t, result, "Result should not be nil")

		// If we have teams, verify they have the expected fields
		if len(result) > 0 {
			team := result[0]
			assert.NotEmpty(t, team.ID, "Team should have an ID")
			assert.NotEmpty(t, team.Name, "Team should have a name")
		}
	})
}
