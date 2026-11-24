//go:build unit

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDbo11yAPI answers the two routes the fixtures read, by path, and records
// what it was asked.
func fakeDbo11yAPI(responses map[string]string, calls *[]string) rbacAPI {
	return func(method, path string, body, out any) error {
		*calls = append(*calls, method+" "+path)
		resp, ok := responses[path]
		if !ok {
			return errors.New("404 not found")
		}
		return json.Unmarshal([]byte(resp), out)
	}
}

// runFixture runs setup in a subtest, as TestToolPermissions does, and reports
// the arguments it returned and whether it skipped the test.
func runFixture(t *testing.T, setup func(*testing.T, rbacAPI) map[string]any, api rbacAPI) (args map[string]any, skipped bool) {
	t.Helper()
	var sub *testing.T
	t.Run("fixture", func(t *testing.T) {
		sub = t
		args = setup(t, api)
	})
	return args, sub.Skipped()
}

func TestRBACDbo11yFixtures(t *testing.T) {
	const instances = rbacDbo11yPath + "/instances"

	t.Run("an instance fixture picks the first monitored instance", func(t *testing.T) {
		var calls []string
		api := fakeDbo11yAPI(map[string]string{instances: `{"status":"success","data":[{"server_id":"a"},{"server_id":"b"}]}`}, &calls)
		args, skipped := runFixture(t, rbacDbo11yInstanceArgs, api)
		assert.False(t, skipped)
		assert.Equal(t, map[string]any{"server_id": "a"}, args)
		assert.Equal(t, []string{"GET " + instances}, calls)
	})

	t.Run("a stack with no monitored instance skips", func(t *testing.T) {
		var calls []string
		api := fakeDbo11yAPI(map[string]string{instances: `{"status":"success","data":[]}`}, &calls)
		_, skipped := runFixture(t, rbacDbo11yInstanceArgs, api)
		assert.True(t, skipped)
	})

	t.Run("an API that can't be read skips instead of failing", func(t *testing.T) {
		var calls []string
		_, skipped := runFixture(t, rbacDbo11yInstanceArgs, fakeDbo11yAPI(nil, &calls))
		assert.True(t, skipped)
		_, skipped = runFixture(t, rbacDbo11yQueryArgs, fakeDbo11yAPI(nil, &calls))
		assert.True(t, skipped)
	})

	queriesOf := func(serverID string) string { return rbacDbo11yPath + "/instances/" + serverID + "/queries?limit=1" }

	t.Run("a MySQL statement is addressed by its digest", func(t *testing.T) {
		var calls []string
		api := fakeDbo11yAPI(map[string]string{
			instances:         `{"data":[{"server_id":"my-1"}]}`,
			queriesOf("my-1"): `{"data":{"queries":[{"digest":"abc123","database":"shop"}]}}`,
		}, &calls)
		args, skipped := runFixture(t, rbacDbo11yQueryArgs, api)
		require.False(t, skipped)
		assert.Equal(t, map[string]any{"server_id": "my-1", "query_id": "abc123", "database": "shop"}, args)
	})

	t.Run("a Postgres statement is addressed by its query_id and database", func(t *testing.T) {
		var calls []string
		api := fakeDbo11yAPI(map[string]string{
			instances:         `{"data":[{"server_id":"pg-1"}]}`,
			queriesOf("pg-1"): `{"data":{"queries":[{"query_id":"-4023954832","database":"orders"}]}}`,
		}, &calls)
		args, skipped := runFixture(t, rbacDbo11yQueryArgs, api)
		require.False(t, skipped)
		assert.Equal(t, map[string]any{"server_id": "pg-1", "query_id": "-4023954832", "database": "orders"}, args)
	})

	t.Run("an instance that ran no query skips", func(t *testing.T) {
		var calls []string
		api := fakeDbo11yAPI(map[string]string{
			instances:           `{"data":[{"server_id":"idle-1"}]}`,
			queriesOf("idle-1"): `{"data":{"queries":[]}}`,
		}, &calls)
		_, skipped := runFixture(t, rbacDbo11yQueryArgs, api)
		assert.True(t, skipped)
	})

	t.Run("no fixture is marked skip any more", func(t *testing.T) {
		for _, name := range []string{
			"list_dbo11y_instances", "get_dbo11y_instance", "get_dbo11y_instance_detail",
			"list_dbo11y_top_queries", "get_dbo11y_query_detail", "list_dbo11y_recommendations",
		} {
			f, ok := rbacFixtures[name]
			require.True(t, ok, name)
			assert.Empty(t, f.skip, name)
			assert.True(t, f.setup != nil || f.args != nil, name)
		}
	})
}
