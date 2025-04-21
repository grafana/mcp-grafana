// Requires a Grafana instance running on localhost:3000,
// with the Asserts plugin installed and configured.
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssertTools(t *testing.T) {
	t.Run("get assertions", func(t *testing.T) {
		ctx := newTestContext()
		result, err := getAssertions(ctx, GetAssertionsParams{
			EntityName: "test",
			EntityType: "test",
			Env:        "test",
			Site:       "test",
			Namespace:  "test",
			StartTime:  1713571200,
			EndTime:    1713657600,
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}
