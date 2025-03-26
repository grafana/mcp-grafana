//go:build cloud
// +build cloud

// This file contains cloud integration tests that run against a dedicated test instance
// at mcptests.grafana-dev.net. This instance is configured with a minimal setup on the OnCall side:
//   - One team
//   - Two schedules (only one has a team assigned)
//   - One shift in the schedule with a team assigned
//   - One user
// These tests expect this configuration to exist and will skip if the required
// environment variables (GRAFANA_URL, GRAFANA_API_KEY) are not set.

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

	if len(schedules) > 0 && schedules[0].TeamID != "" {
		teamID := schedules[0].TeamID

		// Test filtering by team ID
		t.Run("list schedules by team ID", func(t *testing.T) {
			result, err := listOnCallSchedules(ctx, ListOnCallSchedulesParams{
				TeamID: teamID,
			})
			require.NoError(t, err, "Should not error when listing schedules by team")
			assert.NotEmpty(t, result, "Should return at least one schedule")
			for _, schedule := range result {
				assert.Equal(t, teamID, schedule.TeamID, "All schedules should belong to the specified team")
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
	require.NotEmpty(t, schedules[0].Shifts, "Schedule should have at least one shift")

	shifts := schedules[0].Shifts
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
		assert.Equal(t, scheduleID, result.ScheduleID, "Should return the correct schedule")
		assert.NotNil(t, result.Users, "Users field should be present")
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

func TestCloudOnCallUsers(t *testing.T) {
	ctx := createOnCallCloudTestContext(t)

	t.Run("list all users", func(t *testing.T) {
		result, err := listOnCallUsers(ctx, ListOnCallUsersParams{})
		require.NoError(t, err, "Should not error when listing users")

		// We can't assert exact counts since we're using a real instance,
		// but we can check that the call succeeded and returned data
		assert.NotNil(t, result, "Result should not be nil")

		// If we have users, verify they have the expected fields
		if len(result) > 0 {
			user := result[0]
			assert.NotEmpty(t, user.ID, "User should have an ID")
			assert.NotEmpty(t, user.Username, "User should have a username")
		}
	})

	// Get a user ID from the list to test filtering
	users, err := listOnCallUsers(ctx, ListOnCallUsersParams{})
	require.NoError(t, err, "Should not error when listing users")
	require.NotEmpty(t, users, "Should have at least one user to test with")

	userID := users[0].ID

	t.Run("get user by ID", func(t *testing.T) {
		result, err := listOnCallUsers(ctx, ListOnCallUsersParams{
			UserID: userID,
		})
		require.NoError(t, err, "Should not error when getting user by ID")
		assert.NotNil(t, result, "Result should not be nil")
		assert.Len(t, result, 1, "Should return exactly one user")
		assert.Equal(t, userID, result[0].ID, "Should return the correct user")
		assert.NotEmpty(t, result[0].Username, "User should have a username")
	})

	t.Run("get user with invalid ID", func(t *testing.T) {
		_, err := listOnCallUsers(ctx, ListOnCallUsersParams{
			UserID: "invalid-user-id",
		})
		assert.Error(t, err, "Should error when getting user with invalid ID")
	})
}
