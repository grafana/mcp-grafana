// Requires a Grafana instance running on localhost:3000,
// with alert rules provisioned.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/stretchr/testify/require"
)

// uniqueSilenceMarker returns a label value that is unique per test run.
// Expiring a silence does not remove it, so the test Grafana accumulates
// silences across runs; filtering on a unique marker keeps assertions exact.
func uniqueSilenceMarker() string {
	return fmt.Sprintf("mcp-silences-%d", time.Now().UnixNano())
}

// createTestSilence creates a silence and registers a cleanup that expires it.
func createTestSilence(ctx context.Context, t *testing.T, params ManageSilencesParams) string {
	t.Helper()

	result, err := manageSilencesReadWrite(ctx, params)
	require.NoError(t, err)
	created, ok := result.(*createSilenceResponse)
	require.True(t, ok, "unexpected create result type %T", result)
	require.NotEmpty(t, created.SilenceID)

	expireSilence(ctx, t, created.SilenceID)
	return created.SilenceID
}

func expireSilence(ctx context.Context, t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "delete",
			SilenceID: &id,
		})
	})
}

func TestManageSilencesLifecycle(t *testing.T) {
	ctx := newTestContext()

	marker := uniqueSilenceMarker()
	matchers := []SilenceMatcherParam{{Name: "mcp_test_marker", Value: marker}}
	// Start slightly in the past so the silence is immediately active rather
	// than pending: Alertmanager only allows in-place updates of active or
	// pending silences, and an active one exercises the stricter path.
	startsAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	endsAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	comment := "mcp-grafana integration test"

	silenceID := createTestSilence(ctx, t, ManageSilencesParams{
		Operation: "create",
		Matchers:  matchers,
		StartsAt:  &startsAt,
		EndsAt:    &endsAt,
		Comment:   &comment,
	})

	t.Run("get returns the created silence", func(t *testing.T) {
		result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "get",
			SilenceID: &silenceID,
		})
		require.NoError(t, err)

		silence, ok := result.(*models.GettableSilence)
		require.True(t, ok, "unexpected get result type %T", result)
		require.Equal(t, silenceID, *silence.ID)
		require.Equal(t, comment, *silence.Comment)
		require.Equal(t, defaultSilenceCreatedBy, *silence.CreatedBy)
		require.Equal(t, "active", *silence.Status.State)
		require.Len(t, silence.Matchers, 1)
		require.Equal(t, "mcp_test_marker", *silence.Matchers[0].Name)
		require.Equal(t, marker, *silence.Matchers[0].Value)
		require.False(t, *silence.Matchers[0].IsRegex)
	})

	t.Run("list filtered by matcher finds exactly this silence", func(t *testing.T) {
		result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "list",
			Matchers:  matchers,
		})
		require.NoError(t, err)

		silences, ok := result.(models.GettableSilences)
		require.True(t, ok, "unexpected list result type %T", result)
		require.Len(t, silences, 1)
		require.Equal(t, silenceID, *silences[0].ID)
	})

	t.Run("list without filters includes this silence", func(t *testing.T) {
		result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{Operation: "list"})
		require.NoError(t, err)

		silences, ok := result.(models.GettableSilences)
		require.True(t, ok, "unexpected list result type %T", result)
		require.Contains(t, silenceIDs(silences), silenceID)
	})

	t.Run("update with the stored starts_at keeps the id", func(t *testing.T) {
		// Alertmanager only updates an active silence in place when the posted
		// matchers and starts_at match the stored ones. Grafana clamps a
		// starts_at in the past to the creation time, so the stored value has
		// to be read back rather than reusing what we posted on create.
		storedStartsAt := getSilenceStartsAt(ctx, t, silenceID)
		newEndsAt := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
		newComment := comment + " (updated)"

		result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "update",
			SilenceID: &silenceID,
			Matchers:  matchers,
			StartsAt:  &storedStartsAt,
			EndsAt:    &newEndsAt,
			Comment:   &newComment,
		})
		require.NoError(t, err)

		updated, ok := result.(*createSilenceResponse)
		require.True(t, ok, "unexpected update result type %T", result)
		require.Equal(t, silenceID, updated.SilenceID)

		got, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "get",
			SilenceID: &silenceID,
		})
		require.NoError(t, err)
		silence, ok := got.(*models.GettableSilence)
		require.True(t, ok, "unexpected get result type %T", got)
		require.Equal(t, newComment, *silence.Comment)
		require.Equal(t, "active", *silence.Status.State)
		wantEnd, err := time.Parse(time.RFC3339, newEndsAt)
		require.NoError(t, err)
		require.True(t, time.Time(*silence.EndsAt).Equal(wantEnd),
			"want ends_at %s, got %s", wantEnd, time.Time(*silence.EndsAt))
	})

	t.Run("delete expires the silence rather than removing it", func(t *testing.T) {
		result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "delete",
			SilenceID: &silenceID,
		})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"status": "deleted", "silence_id": silenceID}, result)

		got, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "get",
			SilenceID: &silenceID,
		})
		require.NoError(t, err)
		silence, ok := got.(*models.GettableSilence)
		require.True(t, ok, "unexpected get result type %T", got)
		require.Equal(t, "expired", *silence.Status.State)
	})
}

// TestManageSilencesUpdateReplacesOnChangedStartsAt pins the other half of the
// Alertmanager update contract: anything the API cannot apply in place expires
// the existing silence and returns a new id, rather than failing.
func TestManageSilencesUpdateReplacesOnChangedStartsAt(t *testing.T) {
	ctx := newTestContext()

	marker := uniqueSilenceMarker()
	matchers := []SilenceMatcherParam{{Name: "mcp_test_marker", Value: marker}}
	startsAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	endsAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	comment := "mcp-grafana integration test (replace)"

	originalID := createTestSilence(ctx, t, ManageSilencesParams{
		Operation: "create",
		Matchers:  matchers,
		StartsAt:  &startsAt,
		EndsAt:    &endsAt,
		Comment:   &comment,
	})

	// Posting back the starts_at we originally sent is exactly the case that
	// cannot be applied in place, because Grafana stored the clamped value.
	require.NotEqual(t, startsAt, getSilenceStartsAt(ctx, t, originalID),
		"expected Grafana to clamp a past starts_at to the creation time")

	newEndsAt := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
		Operation: "update",
		SilenceID: &originalID,
		Matchers:  matchers,
		StartsAt:  &startsAt,
		EndsAt:    &newEndsAt,
		Comment:   &comment,
	})
	require.NoError(t, err)

	replacement, ok := result.(*createSilenceResponse)
	require.True(t, ok, "unexpected update result type %T", result)
	require.NotEqual(t, originalID, replacement.SilenceID)
	expireSilence(ctx, t, replacement.SilenceID)

	// The superseded silence is expired, not deleted, so the window is never
	// covered by two active silences at once.
	got, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
		Operation: "get",
		SilenceID: &originalID,
	})
	require.NoError(t, err)
	silence, ok := got.(*models.GettableSilence)
	require.True(t, ok, "unexpected get result type %T", got)
	require.Equal(t, "expired", *silence.Status.State)
}

func TestManageSilencesRuleUIDFilter(t *testing.T) {
	ctx := newTestContext()

	marker := uniqueSilenceMarker()
	startsAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	endsAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	comment := "mcp-grafana integration test (rule scoped)"

	silenceID := createTestSilence(ctx, t, ManageSilencesParams{
		Operation: "create",
		Matchers: []SilenceMatcherParam{
			{Name: ruleUIDLabel, Value: rule1UID},
			{Name: "mcp_test_marker", Value: marker},
		},
		StartsAt: &startsAt,
		EndsAt:   &endsAt,
		Comment:  &comment,
	})

	// rule_uid must be translated into an __alert_rule_uid__ matcher. Combining
	// it with the unique marker keeps the assertion exact despite leftovers
	// from earlier runs that are scoped to the same rule.
	ruleUID := rule1UID
	result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
		Operation: "list",
		RuleUID:   &ruleUID,
		Matchers:  []SilenceMatcherParam{{Name: "mcp_test_marker", Value: marker}},
	})
	require.NoError(t, err)

	silences, ok := result.(models.GettableSilences)
	require.True(t, ok, "unexpected list result type %T", result)
	require.Len(t, silences, 1)
	require.Equal(t, silenceID, *silences[0].ID)

	// A rule UID that no silence is scoped to must not match ours.
	otherUID := "no-such-rule-uid"
	result, err = manageSilencesReadWrite(ctx, ManageSilencesParams{
		Operation: "list",
		RuleUID:   &otherUID,
		Matchers:  []SilenceMatcherParam{{Name: "mcp_test_marker", Value: marker}},
	})
	require.NoError(t, err)
	silences, ok = result.(models.GettableSilences)
	require.True(t, ok, "unexpected list result type %T", result)
	require.Empty(t, silences)
}

func TestManageSilencesReadOnlyVariant(t *testing.T) {
	ctx := newTestContext()

	t.Run("list is served", func(t *testing.T) {
		result, err := manageSilencesRead(ctx, ManageSilencesReadParams{Operation: "list"})
		require.NoError(t, err)
		_, ok := result.(models.GettableSilences)
		require.True(t, ok, "unexpected list result type %T", result)
	})

	t.Run("write operations are not reachable", func(t *testing.T) {
		for _, op := range []string{"create", "update", "delete"} {
			_, err := manageSilencesRead(ctx, ManageSilencesReadParams{Operation: op})
			require.ErrorContains(t, err, "unknown operation")
		}
	})
}

func TestManageSilencesErrors(t *testing.T) {
	ctx := newTestContext()

	t.Run("get with an unknown silence id", func(t *testing.T) {
		unknown := "00000000-0000-0000-0000-000000000000"
		_, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "get",
			SilenceID: &unknown,
		})
		require.ErrorContains(t, err, "failed to get silence")
	})

	t.Run("create with an already-expired window is rejected before the API call", func(t *testing.T) {
		startsAt := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
		endsAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		comment := "should never be created"
		_, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
			Operation: "create",
			Matchers:  []SilenceMatcherParam{{Name: "mcp_test_marker", Value: uniqueSilenceMarker()}},
			StartsAt:  &startsAt,
			EndsAt:    &endsAt,
			Comment:   &comment,
		})
		require.ErrorContains(t, err, "is in the past")
	})
}

// getSilenceStartsAt reads back the starts_at Grafana actually stored, which
// differs from the posted value whenever the caller asked for a start in the
// past.
func getSilenceStartsAt(ctx context.Context, t *testing.T, id string) string {
	t.Helper()

	result, err := manageSilencesReadWrite(ctx, ManageSilencesParams{
		Operation: "get",
		SilenceID: &id,
	})
	require.NoError(t, err)
	silence, ok := result.(*models.GettableSilence)
	require.True(t, ok, "unexpected get result type %T", result)
	require.NotNil(t, silence.StartsAt)
	return time.Time(*silence.StartsAt).UTC().Format(time.RFC3339Nano)
}

func silenceIDs(silences models.GettableSilences) []string {
	ids := make([]string, 0, len(silences))
	for _, s := range silences {
		ids = append(ids, *s.ID)
	}
	return ids
}
