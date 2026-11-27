//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockDbo11yServer(handler http.HandlerFunc) (*httptest.Server, context.Context) {
	server := httptest.NewServer(handler)
	config := mcpgrafana.GrafanaConfig{
		URL:    server.URL,
		APIKey: "test-api-key",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	return server, ctx
}

// dbo11yNeverCalledContext is a context for a server that fails the test if it
// is asked for anything, for cases that must be rejected before any request.
func dbo11yNeverCalledContext(t *testing.T) context.Context {
	t.Helper()
	server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server must not be called, got %s %s", r.Method, r.URL)
	})
	t.Cleanup(server.Close)
	return ctx
}

func TestDbo11yTools(t *testing.T) {
	t.Run("list instances forwards every documented filter", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances", r.URL.Path)
			assert.Equal(t, "mysql-prod-1", r.URL.Query().Get("server_id"))
			assert.Equal(t, "8.0.35", r.URL.Query().Get("engine_version"))
			assert.Equal(t, "aws", r.URL.Query().Get("provider_name"))
			assert.Equal(t, "123456789012", r.URL.Query().Get("provider_account"))
			assert.Equal(t, "eu-west-1", r.URL.Query().Get("provider_region"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":[
				{"server_id":"mysql-prod-1","instance":"mysql-prod-1.rds.amazonaws.com:3306","engine":"mysql","engine_version":"8.0.35",
				 "cloud_provider":{"aws":{"db_instance_id":"mysql-prod-1","account_id":"123456789012","region":"eu-west-1"}}}
			]}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{
			ServerID: "mysql-prod-1", EngineVersion: "8.0.35",
			ProviderName: "aws", ProviderAccount: "123456789012", ProviderRegion: "eu-west-1",
		})
		require.NoError(t, err)
		assert.Equal(t, 1, result.Total)
		assert.Equal(t, 1, result.Returned)
		assert.False(t, result.Truncated)
		require.Len(t, result.Instances, 1)
		inst := result.Instances[0]
		assert.Equal(t, "mysql-prod-1", inst.ServerID)
		assert.Equal(t, "mysql", inst.Engine)
		require.NotNil(t, inst.CloudProvider)
		require.NotNil(t, inst.CloudProvider.AWS)
		assert.Equal(t, "eu-west-1", inst.CloudProvider.AWS.Region)
		assert.Nil(t, inst.Metrics, "listInstances is metadata-only; metrics should never be populated")
	})

	t.Run("get instance with metrics", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/mysql-prod-1", r.URL.Path)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":
				{"server_id":"mysql-prod-1","engine":"mysql","metrics":{"qps":142.7,"avg_latency_ms":3.42,"error_rate":0.0031,"cpu_percent":37.5}}
			}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "mysql-prod-1"})
		require.NoError(t, err)
		require.NotNil(t, result.Metrics)
		require.NotNil(t, result.Metrics.CPUPercent)
		assert.Equal(t, 37.5, *result.Metrics.CPUPercent)
	})

	t.Run("instance metrics distinguishes an explicit zero from an omitted field", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// error_rate is explicitly 0 (a real, reported value); cpu_percent is
			// wholly absent (not reported). Dbo11yMetrics's doc comment promises
			// these stay distinct — this is what actually exercises that promise.
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql","metrics":{"qps":10.0,"error_rate":0}}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "mysql-prod-1"})
		require.NoError(t, err)
		require.NotNil(t, result.Metrics)
		require.NotNil(t, result.Metrics.ErrorRate, "error_rate was explicitly 0 in the payload and must decode as a present pointer, not be treated as absent")
		assert.Equal(t, 0.0, *result.Metrics.ErrorRate)
		assert.Nil(t, result.Metrics.CPUPercent, "cpu_percent was omitted from the payload and must decode as nil, not as a zero value")
	})

	t.Run("list top queries applies the tool's own smaller default limit", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/mysql-prod-1/queries", r.URL.Path)
			// The API's own default is 30; this tool defaults to 10 so a single
			// call can't return more rows than a model should read unprompted.
			assert.Equal(t, "10", r.URL.Query().Get("limit"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql","queries":[
				{"digest":"abc123","database":"orders","query_text":"SELECT * FROM orders WHERE id = ?","db_load":0.42,"calls":12500}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "mysql-prod-1"})
		require.NoError(t, err)
		require.Len(t, result.Queries, 1)
		assert.Equal(t, "abc123", result.Queries[0].Digest)
		assert.Equal(t, 0.42, result.Queries[0].DBLoad)
		require.NotNil(t, result.Queries[0].Calls)
		assert.Equal(t, float64(12500), *result.Queries[0].Calls)
	})

	t.Run("list top queries respects an explicit limit", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "25", r.URL.Query().Get("limit"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","queries":[]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "mysql-prod-1", Limit: 25})
		require.NoError(t, err)
	})

	t.Run("list top queries decodes a Postgres-shaped row by query_id", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"pg-1","engine":"postgres","queries":[
				{"query_id":"-4023954832","database":"shop","query_text":"SELECT * FROM orders WHERE id = $1","db_load":0.18}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "pg-1"})
		require.NoError(t, err)
		require.Len(t, result.Queries, 1)
		assert.Equal(t, "-4023954832", result.Queries[0].QueryID)
		assert.Empty(t, result.Queries[0].Digest, "a Postgres row has no digest")
	})

	t.Run("query detail 'samples' applies the tool's own smaller default limit and forwards database/engine", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/pg-1/queries/-4023954832/samples", r.URL.Path)
			// The API's own default is 20; this tool defaults to 5.
			assert.Equal(t, "5", r.URL.Query().Get("limit"))
			assert.Equal(t, "shop", r.URL.Query().Get("database"))
			assert.Equal(t, "postgres", r.URL.Query().Get("engine"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"pg-1","engine":"postgres","query_id":"-4023954832","database":"shop","truncated":false,"samples":[
				{"timestamp":"2026-09-28T10:00:00Z","duration_seconds":0.012,"sql_text":"SELECT * FROM orders WHERE id = $1","fields":{"backend_type":"client backend"}}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{
			Operation: "samples", ServerID: "pg-1", QueryID: "-4023954832", Database: "shop", Engine: "postgres",
		})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yQuerySamples)
		require.True(t, ok, "operation=samples must return Dbo11yQuerySamples")
		require.Len(t, result.Samples, 1)
		require.NotNil(t, result.Samples[0].DurationSeconds)
		assert.Equal(t, 0.012, *result.Samples[0].DurationSeconds)
		assert.Equal(t, "client backend", result.Samples[0].Fields["backend_type"])
		assert.False(t, result.Truncated)
	})

	t.Run("query detail 'explain_plan' decodes an arbitrary engine-specific plan", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/mysql-prod-1/queries/abc123/explain-plan", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql","query_id":"abc123","timestamp":"2026-09-28T10:00:00Z","plan":{"query_block":{"select_id":1}}}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "explain_plan", ServerID: "mysql-prod-1", QueryID: "abc123"})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yExplainPlan)
		require.True(t, ok, "operation=explain_plan must return Dbo11yExplainPlan")
		assert.Equal(t, `{"query_block":{"select_id":1}}`, string(result.Plan))
	})

	t.Run("query detail 'explain_plan' passes the plan through byte for byte", func(t *testing.T) {
		const plan = `{"z":1,"a":[12345678901234567890,1.0,{"n":null}],"Plan":{"Node Type":"Seq Scan"}}`
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"pg-1","engine":"postgres","query_id":"1","timestamp":"2026-09-28T10:00:00Z","plan":` + plan + `}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "explain_plan", ServerID: "pg-1", QueryID: "1", Database: "shop"})
		require.NoError(t, err)
		assert.Equal(t, plan, string(raw.(Dbo11yExplainPlan).Plan), "keys must keep their order and large numbers their digits")
		out, err := json.Marshal(raw)
		require.NoError(t, err)
		assert.Contains(t, string(out), `"plan":`+plan)
	})

	t.Run("query detail 'wait_events' decodes the ordered, non-zero breakdown returned by the API", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/mysql-prod-1/queries/abc123/wait-events", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql","query_id":"abc123","total_wait_seconds":5.2,"wait_events":[
				{"type":"Lock Wait","wait_seconds":4.1},
				{"type":"IO Wait","wait_seconds":1.1}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "wait_events", ServerID: "mysql-prod-1", QueryID: "abc123"})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yWaitEvents)
		require.True(t, ok, "operation=wait_events must return Dbo11yWaitEvents")
		require.Len(t, result.WaitEvents, 2)
		assert.Equal(t, "Lock Wait", result.WaitEvents[0].Type)
		assert.Equal(t, 5.2, result.TotalWaitSeconds)
	})

	t.Run("query detail 'schema' keeps a table with no spec or DDL rather than dropping it", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/mysql-prod-1/queries/abc123/schema", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql","query_id":"abc123","truncated":false,"tables":[
				{"table":"orders","database":"shop","create_ddl":"CREATE TABLE orders (...)","spec":{"columns":[{"name":"id"}]}},
				{"table":"unknown_table","database":"shop"}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "schema", ServerID: "mysql-prod-1", QueryID: "abc123"})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yQuerySchema)
		require.True(t, ok, "operation=schema must return Dbo11yQuerySchema")
		require.Len(t, result.Tables, 2)
		assert.NotEmpty(t, result.Tables[0].CreateDDL)
		assert.Equal(t, `{"columns":[{"name":"id"}]}`, string(result.Tables[0].Spec))
		assert.Empty(t, result.Tables[1].Spec)
		assert.Empty(t, result.Tables[1].CreateDDL, "a table with no captured spec should stay in the list with empty fields, not be dropped")
	})

	t.Run("query detail rejects an unknown operation without calling the server", func(t *testing.T) {
		ctx := dbo11yNeverCalledContext(t)

		_, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "bogus", ServerID: "mysql-prod-1", QueryID: "abc123"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown operation")
	})

	t.Run("a documented not-found error surfaces the code and message", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, err := w.Write([]byte(`{"status":"error","error":{"code":"explain_plan_not_found","message":"no explain plan was captured for this query in the requested window"}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "explain_plan", ServerID: "mysql-prod-1", QueryID: "abc123"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "explain_plan_not_found")
		assert.Contains(t, err.Error(), "no explain plan was captured")
	})

	t.Run("a bare 404 from an unmounted route falls back to the raw body", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			// Grafana's router returns this bare text/plain 404, not a JSON error
			// envelope, when the dbo11y data routes were never mounted at all.
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, err := w.Write([]byte("404 page not found\n"))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404 page not found")
	})

	t.Run("list instances forwards free-form label filters and decodes labels", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "payments", q.Get("team"))
			assert.Equal(t, "staging", q.Get("deployment_environment"))
			assert.Equal(t, "postgres", q.Get("engine"), "a typed filter must win over a label of the same name")
			assert.Equal(t, "my-prom", q.Get("datasource"))
			assert.False(t, q.Has("empty"), "a label with an empty value must not be sent")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":[
				{"server_id":"pg-selfmanaged-1","instance":"db.internal:5432","engine":"postgres","engine_version":"15.6",
				 "labels":{"deployment_environment":"staging","service_name":"checkout","team":"payments"}}
			]}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{
			Engine:     "postgres",
			Datasource: "my-prom",
			Labels:     map[string]string{"team": "payments", "deployment_environment": "staging", "engine": "mysql", "empty": ""},
		})
		require.NoError(t, err)
		require.Len(t, result.Instances, 1)
		assert.Equal(t, map[string]string{"deployment_environment": "staging", "service_name": "checkout", "team": "payments"}, result.Instances[0].Labels)
	})

	t.Run("list instances rejects a label named after a request parameter without calling the server", func(t *testing.T) {
		ctx := dbo11yNeverCalledContext(t)

		for _, reserved := range []string{"datasource", "from", "to"} {
			_, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{Labels: map[string]string{reserved: "x"}})
			require.Error(t, err, reserved)
			assert.Contains(t, err.Error(), "reserved")
		}
	})

	t.Run("get instance forwards the datasource override", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "my-prom", r.URL.Query().Get("datasource"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql"}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "mysql-prod-1", Datasource: "my-prom"})
		require.NoError(t, err)
	})

	t.Run("list top queries decodes rows_examined and forwards both data source overrides", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "my-prom", r.URL.Query().Get("datasource"))
			assert.Equal(t, "my-loki", r.URL.Query().Get("loki"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql","queries":[
				{"digest":"abc123","db_load":0.42,"calls":900,"rows_examined":45230,"error_rate":0},
				{"digest":"def456","db_load":0.05}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "mysql-prod-1", Datasource: "my-prom", Loki: "my-loki"})
		require.NoError(t, err)
		require.Len(t, result.Queries, 2)
		require.NotNil(t, result.Queries[0].RowsExamined)
		assert.Equal(t, float64(45230), *result.Queries[0].RowsExamined)
		require.NotNil(t, result.Queries[0].ErrorRate, "an explicit 0 error_rate is a reported value")
		assert.Nil(t, result.Queries[1].RowsExamined, "an omitted rows_examined must stay nil rather than read as 0")
	})

	t.Run("instance detail 'sessions' decodes a Postgres instance and keeps an explicit zero", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/pg-analytics/sessions", r.URL.Path)
			assert.Equal(t, "my-prom", r.URL.Query().Get("datasource"))
			assert.False(t, r.URL.Query().Has("limit"), "sessions takes no limit")
			assert.False(t, r.URL.Query().Has("loki"), "sessions reads no logs")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"pg-analytics","engine":"postgres","total":25,"max_connections":429,
				"by_state":{"active":9,"idle":16},"longest_transaction_seconds":0}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "sessions", ServerID: "pg-analytics", Datasource: "my-prom", Limit: 50, Loki: "my-loki"})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yInstanceSessions)
		require.True(t, ok, "operation=sessions must return Dbo11yInstanceSessions")
		require.NotNil(t, result.Total)
		assert.Equal(t, float64(25), *result.Total)
		assert.Equal(t, map[string]float64{"active": 9, "idle": 16}, result.ByState)
		require.NotNil(t, result.LongestTransactionSeconds, "a reported 0 means no transaction is open and must not read as absent")
		assert.Equal(t, 0.0, *result.LongestTransactionSeconds)
	})

	t.Run("instance detail 'sessions' for an engine that reports none carries identity only", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"sqlserver-lab-1","engine":"sql_server"}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "sessions", ServerID: "sqlserver-lab-1"})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yInstanceSessions)
		require.True(t, ok)
		assert.Equal(t, "sql_server", result.Engine)
		assert.Nil(t, result.Total, "SQL Server reports no session count; that is not a count of 0")
		assert.Nil(t, result.MaxConnections)
		assert.Empty(t, result.ByState)
	})

	t.Run("instance detail 'wait_events' applies its own default limit and decodes events", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/pg-analytics/wait-events", r.URL.Path)
			// The API's own default is 50; this tool defaults to 10.
			assert.Equal(t, "10", r.URL.Query().Get("limit"))
			assert.Equal(t, "my-prom", r.URL.Query().Get("datasource"), "this route reads both upstreams")
			assert.Equal(t, "my-loki", r.URL.Query().Get("loki"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"pg-analytics","total_wait_seconds":21469.85,"wait_events":[
				{"type":"Engine Wait","event":"BufferMapping","wait_seconds":10702.7,"occurrences":912},
				{"type":"IO Wait","event":"DataFileRead","wait_seconds":10279.04,"occurrences":48213,"waiting_sessions":7}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "wait_events", ServerID: "pg-analytics", Datasource: "my-prom", Loki: "my-loki"})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yInstanceWaitEvents)
		require.True(t, ok, "operation=wait_events must return Dbo11yInstanceWaitEvents")
		require.Len(t, result.WaitEvents, 2)
		assert.Equal(t, "BufferMapping", result.WaitEvents[0].Event)
		assert.Nil(t, result.WaitEvents[0].WaitingSessions, "waiting_sessions is only present when a session is blocked right now")
		require.NotNil(t, result.WaitEvents[1].WaitingSessions)
		assert.Equal(t, float64(7), *result.WaitEvents[1].WaitingSessions)
		assert.Equal(t, 21469.85, result.TotalWaitSeconds)
	})

	t.Run("instance detail 'wait_events' respects an explicit limit", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "75", r.URL.Query().Get("limit"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"pg-analytics","wait_events":[],"total_wait_seconds":0}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "wait_events", ServerID: "pg-analytics", Limit: 75})
		require.NoError(t, err)
	})

	t.Run("instance detail rejects an unknown operation without calling the server", func(t *testing.T) {
		ctx := dbo11yNeverCalledContext(t)

		_, err := getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "timeline", ServerID: "pg-analytics"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown operation")
	})

	t.Run("query detail 'timeline' reads Prometheus, forwards step, and keeps absent metrics nil", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/instances/mysql-prod-1/queries/abc123/timeline", r.URL.Path)
			assert.Equal(t, "5m", r.URL.Query().Get("step"))
			assert.Equal(t, "my-prom", r.URL.Query().Get("datasource"))
			assert.False(t, r.URL.Query().Has("loki"), "timeline reads Prometheus alone")
			assert.False(t, r.URL.Query().Has("limit"), "timeline takes no limit")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"mysql-prod-1","engine":"mysql","query_id":"abc123","step_seconds":300,"points":[
				{"timestamp":1755594300,"calls":120,"avg_latency_ms":3.2,"error_rate":0},
				{"timestamp":1755594600,"calls":0}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		raw, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{
			Operation: "timeline", ServerID: "mysql-prod-1", QueryID: "abc123",
			Step: "5m", Datasource: "my-prom", Loki: "my-loki", Limit: 20,
		})
		require.NoError(t, err)
		result, ok := raw.(Dbo11yQueryTimeline)
		require.True(t, ok, "operation=timeline must return Dbo11yQueryTimeline")
		assert.Equal(t, int64(300), result.StepSeconds)
		require.Len(t, result.Points, 2)
		assert.Equal(t, time.Unix(1755594300, 0).UTC(), result.Points[0].WindowEnd)
		out, err := json.Marshal(result.Points[0])
		require.NoError(t, err)
		assert.Contains(t, string(out), `"window_end":"`+time.Unix(1755594300, 0).UTC().Format(time.RFC3339)+`"`, "the end of each window is RFC3339")
		assert.NotContains(t, string(out), `"timestamp"`)
		require.NotNil(t, result.Points[0].ErrorRate, "an explicit 0 error_rate is a reported value")
		require.NotNil(t, result.Points[1].Calls)
		assert.Equal(t, 0.0, *result.Points[1].Calls)
		assert.Nil(t, result.Points[1].AvgLatencyMs, "a metric with no data in the window must stay nil, not read as 0")
	})

	t.Run("query detail log-backed operations forward loki and never datasource or step", func(t *testing.T) {
		for _, op := range []string{"samples", "explain_plan", "wait_events", "schema"} {
			server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "my-loki", r.URL.Query().Get("loki"), op)
				assert.False(t, r.URL.Query().Has("datasource"), op)
				assert.False(t, r.URL.Query().Has("step"), op)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(`{"status":"success","data":{}}`))
				assert.NoError(t, err)
			})

			_, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{
				Operation: op, ServerID: "mysql-prod-1", QueryID: "abc123",
				Loki: "my-loki", Datasource: "my-prom", Step: "5m",
			})
			require.NoError(t, err, op)
			server.Close()
		}
	})

	t.Run("an invalid step error from the API surfaces its code", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, err := w.Write([]byte(`{"status":"error","error":{"code":"invalid_step","message":"step must be a duration with a unit, such as 30s, 5m, 1h or 1d"}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "timeline", ServerID: "mysql-prod-1", QueryID: "abc123", Step: "300"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid_step")
	})
}

func TestDbo11yRecommendations(t *testing.T) {
	const listBody = `{"status":"success","data":{
		"window":{"from":"2026-08-19T09:00:00Z","to":"2026-08-19T10:00:00Z"},
		"total":3,
		"recommendations":[{
			"type":"connection_saturation","category":"resource","severity":"critical","status":"firing","scope":"instance","engine":"postgres",
			"affected_entities":[{"name":"orders-db","server_id":"srv-orders-1","service":"orders-db","env":"111111111111","site":"eu-west-1","provider":"aws"}],
			"streak_started_at":"2026-08-19T08:41:07Z","first_seen":"2026-08-19T09:00:12Z","last_seen":"2026-08-19T09:59:41Z","duration_seconds":4714,
			"detection":{"family":"saturation"},
			"content":{"title":"Connection saturation","summary":"Elevated connection usage relative to the instance limit has been detected"}
		}]}}`

	t.Run("forwards every filter, joins lists and applies the tool's own default limit", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/api/plugins/grafana-dbo11y-app/resources/api/v1/recommendations", r.URL.Path)
			q := r.URL.Query()
			assert.Equal(t, "firing", q.Get("status"))
			assert.Equal(t, "critical,warning", q.Get("severity"))
			assert.Equal(t, "resource", q.Get("category"))
			assert.Equal(t, "connection_saturation,query_latency_spike", q.Get("type"))
			assert.Equal(t, "instance", q.Get("scope"))
			assert.Equal(t, "srv-orders-1", q.Get("server_id"))
			assert.Equal(t, "orders-db", q.Get("service"))
			assert.Equal(t, "111111111111", q.Get("env"))
			assert.Equal(t, "eu-west-1", q.Get("site"))
			assert.Equal(t, "orders-db.example:5432", q.Get("instance"))
			assert.Equal(t, "orders-db-rds", q.Get("db_instance_identifier"))
			assert.Equal(t, "orders", q.Get("database"))
			assert.Equal(t, "-4023954832", q.Get("query_id"))
			assert.Equal(t, "last_seen", q.Get("sort"))
			assert.Equal(t, "my-prom", q.Get("datasource"))
			assert.Equal(t, "2026-08-19T09:00:00Z", q.Get("from"))
			assert.Equal(t, "2026-08-19T10:00:00Z", q.Get("to"))
			assert.Equal(t, "10", q.Get("limit"), "the tool defaults to a smaller page than the API's 30")
			assert.False(t, q.Has("include"), "an unset include must not be sent")
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(listBody))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yRecommendations(ctx, ListDbo11yRecommendationsParams{
			Status: []string{"firing"}, Severity: []string{"critical", " warning "}, Category: []string{"resource"},
			Type: []string{"connection_saturation", "query_latency_spike"}, Scope: []string{"instance"},
			ServerID: "srv-orders-1", Service: "orders-db", Env: "111111111111", Site: "eu-west-1",
			Instance: "orders-db.example:5432", DBInstanceIdentifier: "orders-db-rds", Database: "orders", QueryID: "-4023954832",
			Sort: "last_seen", Datasource: "my-prom", From: "2026-08-19T09:00:00Z", To: "2026-08-19T10:00:00Z",
		})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Total, "total counts matches the limit cut off")
		require.Len(t, result.Recommendations, 1)
		rec := result.Recommendations[0]
		assert.Equal(t, "connection_saturation", rec.Type)
		assert.Equal(t, "firing", rec.Status)
		assert.Equal(t, "postgres", rec.Engine)
		require.Len(t, rec.AffectedEntities, 1)
		assert.Equal(t, "srv-orders-1", rec.AffectedEntities[0].ServerID)
		assert.Equal(t, "aws", rec.AffectedEntities[0].Provider)
		require.NotNil(t, rec.DurationSeconds)
		assert.Equal(t, int64(4714), *rec.DurationSeconds)
		assert.Equal(t, "saturation", rec.Detection.Family)
		assert.Equal(t, "Connection saturation", rec.Content.Title)
		assert.Empty(t, rec.Content.PossibleCauses, "long-form content is only present with include")
		assert.Nil(t, rec.NextSteps, "next_steps was not asked for, so it is absent")
		assert.Nil(t, rec.Queries, "queries was not asked for, so it is absent")
	})

	t.Run("respects an explicit limit and sends nothing for empty lists", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "1", q.Get("limit"))
			for _, name := range []string{"status", "severity", "category", "type", "scope", "include"} {
				assert.False(t, q.Has(name), name)
			}
			_, err := w.Write([]byte(`{"status":"success","data":{"window":{"from":"a","to":"b"},"total":0,"recommendations":[]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yRecommendations(ctx, ListDbo11yRecommendationsParams{Limit: 1, Status: []string{" ", ""}})
		require.NoError(t, err)
		assert.Equal(t, 0, result.Total)
		assert.NotNil(t, result.Recommendations)
		assert.Empty(t, result.Recommendations)
	})

	t.Run("include content and queries decodes the long-form fields", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "content,queries", r.URL.Query().Get("include"))
			_, err := w.Write([]byte(`{"status":"success","data":{"window":{"from":"a","to":"b"},"total":1,"recommendations":[{
				"type":"query_latency_spike","category":"performance","severity":"warning","status":"resolved","scope":"schema","engine":"mysql",
				"affected_entities":[{"name":"shop-db","server_id":"srv-1"}],"database":"shop","query":{"query_id":"abc123"},
				"first_seen":"2026-08-19T09:00:00Z","last_seen":"2026-08-19T09:30:00Z","detection":{"family":"anomaly"},
				"content":{"title":"Query latency spike","summary":"Query latency rose","details":"## Why it matters",
					"possible_causes":[{"title":"Missing index","body":"A scan."}],
					"learn_more":[{"label":"MySQL docs","url":"https://dev.mysql.com/doc/"}]},
				"queries":[{"name":"query_latency","title":"Latency","unit":"s","expr":"up","peak_expr":"max_over_time(up[5m])","peak_from":"2026-08-19T08:50:00Z",
					"baseline":{"title":"Hour before","expr":"up offset 1h"},"note":"all schemas",
					"datasource":{"type":"prometheus","uid":"grafanacloud-prom"},"from":"2026-08-19T08:45:00Z","to":"2026-08-19T09:45:00Z","step_seconds":60}]
			}]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yRecommendations(ctx, ListDbo11yRecommendationsParams{Include: []string{"content", "queries"}})
		require.NoError(t, err)
		rec := result.Recommendations[0]
		assert.Equal(t, "abc123", rec.Query.QueryID)
		assert.Nil(t, rec.DurationSeconds, "an absent duration is not a zero one")
		assert.Equal(t, "## Why it matters", rec.Content.Details)
		require.Len(t, rec.Content.PossibleCauses, 1)
		assert.Equal(t, "Missing index", rec.Content.PossibleCauses[0].Title)
		require.Len(t, rec.Content.LearnMore, 1)
		assert.Equal(t, "https://dev.mysql.com/doc/", rec.Content.LearnMore[0].URL)
		require.NotNil(t, rec.Queries)
		require.Len(t, *rec.Queries, 1)
		q := (*rec.Queries)[0]
		assert.Equal(t, "up", q.Expr)
		assert.Equal(t, "2026-08-19T08:50:00Z", q.PeakFrom)
		require.NotNil(t, q.Baseline)
		assert.Equal(t, "up offset 1h", q.Baseline.Expr)
		assert.Equal(t, "grafanacloud-prom", q.Datasource.UID)
		assert.Equal(t, int64(60), q.StepSeconds)
	})

	t.Run("next_steps asks the API for requests and returns tool calls instead", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "requests", r.URL.Query().Get("include"))
			_, err := w.Write([]byte(`{"status":"success","data":{"window":{"from":"a","to":"b"},"total":2,"recommendations":[
			{"type":"connection_saturation","category":"resource","severity":"critical","status":"firing","scope":"instance",
			 "affected_entities":[{"name":"orders-db","server_id":"srv-orders-1"}],"first_seen":"x","last_seen":"y","detection":{"family":"saturation"},
			 "content":{"title":"t","summary":"s"},
			 "requests":[
			  {"operation_id":"getInstanceSessions","method":"GET","path":"/api/v1/instances/srv-orders-1/sessions","params":{"from":"2026-08-19T08:45:00Z","to":"2026-08-19T10:00:00Z","datasource":"my-prom"},"description":"Open sessions."},
			  {"operation_id":"getInstanceQueries","method":"GET","path":"/api/v1/instances/srv-orders-1/queries","params":{"from":"2026-08-19T08:45:00Z","to":"2026-08-19T10:00:00Z","limit":"10"},"description":"Top statements."},
			  {"operation_id":"getInstanceWaitEvents","method":"GET","path":"/api/v1/instances/srv-orders-1/wait-events","params":{"limit":"10"},"description":"Waits."},
			  {"operation_id":"somethingNew","method":"GET","path":"/api/v1/new","description":"Unknown to the tools."}]},
			{"type":"query_latency_spike","category":"performance","severity":"warning","status":"firing","scope":"schema","engine":"postgres",
			 "affected_entities":[{"name":"pg-1","server_id":"srv-pg-1"}],"database":"shop","query":{"query_id":"-4023954832"},
			 "first_seen":"x","last_seen":"y","detection":{"family":"anomaly"},"content":{"title":"t","summary":"s"},
			 "requests":[
			  {"operation_id":"getQueryTimeline","method":"GET","path":"/api/v1/instances/srv-pg-1/queries/-4023954832/timeline","params":{"engine":"postgres","database":"shop","step":"300s","from":"f","to":"t"},"description":"Timeline."},
			  {"operation_id":"getQuerySamples","method":"GET","path":"/x","params":{"engine":"postgres","database":"shop","limit":"10"},"description":"Samples."}]}
			]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		result, err := listDbo11yRecommendations(ctx, ListDbo11yRecommendationsParams{Include: []string{"next_steps"}})
		require.NoError(t, err)
		require.Len(t, result.Recommendations, 2)

		instance := result.Recommendations[0]
		assert.Nil(t, instance.Requests, "the API's raw requests are replaced, not echoed")
		require.NotNil(t, instance.NextSteps)
		instanceSteps := *instance.NextSteps
		require.Len(t, instanceSteps, 3, "an operation the tools have no answer for is dropped")
		assert.Equal(t, Dbo11yNextStep{
			Tool:        "get_dbo11y_instance_detail",
			Arguments:   map[string]any{"operation": "sessions", "server_id": "srv-orders-1", "from": "2026-08-19T08:45:00Z", "to": "2026-08-19T10:00:00Z", "datasource": "my-prom"},
			Description: "Open sessions.",
		}, instanceSteps[0])
		assert.Equal(t, Dbo11yNextStep{
			Tool:        "list_dbo11y_top_queries",
			Arguments:   map[string]any{"server_id": "srv-orders-1", "from": "2026-08-19T08:45:00Z", "to": "2026-08-19T10:00:00Z", "limit": 10},
			Description: "Top statements.",
		}, instanceSteps[1])
		assert.Equal(t, "wait_events", instanceSteps[2].Arguments["operation"])

		query := result.Recommendations[1]
		require.NotNil(t, query.NextSteps)
		querySteps := *query.NextSteps
		require.Len(t, querySteps, 2)
		assert.Equal(t, Dbo11yNextStep{
			Tool: "get_dbo11y_query_detail",
			Arguments: map[string]any{"operation": "timeline", "server_id": "srv-pg-1", "query_id": "-4023954832",
				"engine": "postgres", "database": "shop", "step": "300s", "from": "f", "to": "t"},
			Description: "Timeline.",
		}, querySteps[0])
		assert.Equal(t, 10, querySteps[1].Arguments["limit"])
	})

	t.Run("next steps need the instance and, for a query tool, the query", func(t *testing.T) {
		req := []dbo11yFollowUpRequest{{OperationID: "getExplainPlan"}, {OperationID: "getInstanceSessions"}}
		assert.Empty(t, Dbo11yRecommendation{Requests: req}.nextSteps(), "no entity")
		assert.Empty(t, Dbo11yRecommendation{Requests: req, AffectedEntities: []Dbo11yRecommendationEntity{{Name: "x"}}}.nextSteps(), "no server_id")
		steps := Dbo11yRecommendation{Requests: req, AffectedEntities: []Dbo11yRecommendationEntity{{ServerID: "s"}}}.nextSteps()
		require.Len(t, steps, 1, "a query-level tool without a query is dropped")
		assert.Equal(t, "get_dbo11y_instance_detail", steps[0].Tool)
	})

	t.Run("an API error surfaces its code and message", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, err := w.Write([]byte(`{"status":"error","error":{"code":"limit_too_high_for_include","message":"limit must be at most 10 when include is set"}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := listDbo11yRecommendations(ctx, ListDbo11yRecommendationsParams{Include: []string{"content"}, Limit: 50})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit_too_high_for_include")
		assert.Contains(t, err.Error(), "limit must be at most 10")
	})
}

// callDbo11yTool runs a tool through its registered handler, with JSON
// arguments as a client would send them, so the schema's names and the
// argument decoding are exercised along with the function behind them.
func callDbo11yTool(ctx context.Context, tool mcpgrafana.Tool, args string) (*mcp.CallToolResult, error) {
	return tool.Handler(ctx, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: tool.Tool.Name, Arguments: json.RawMessage(args)},
	})
}

func dbo11yResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, result.Content, 1)
	return result.Content[0].(*mcp.TextContent).Text
}

func TestDbo11yRequestsOnTheWire(t *testing.T) {
	const base = "/api/plugins/grafana-dbo11y-app/resources/api/v1"
	const recsBody = `{"status":"success","data":{"window":{"from":"a","to":"b"},"total":0,"recommendations":[]}}`
	for _, tc := range []struct {
		name string
		tool mcpgrafana.Tool
		args string
		body string
		want string // the request URI, path and raw query
	}{
		{
			name: "list instances", tool: ListDbo11yInstances, body: `{"status":"success","data":[]}`,
			args: `{"engine":"mysql","labels":{"team":"payments"},"from":"2026-08-19T09:00:00Z","to":"1755594300000"}`,
			want: base + "/instances?engine=mysql&from=2026-08-19T09%3A00%3A00Z&team=payments&to=1755594300000",
		},
		{
			name: "get instance escapes the server_id into one path segment", tool: GetDbo11yInstance,
			body: `{"status":"success","data":{"server_id":"x"}}`,
			args: `{"server_id":"a/b c","datasource":"p","to":"2026-08-19T10:00:00Z"}`,
			want: base + "/instances/a%2Fb%20c?datasource=p&to=2026-08-19T10%3A00%3A00Z",
		},
		{
			name: "instance sessions", tool: GetDbo11yInstanceDetail, body: `{"status":"success","data":{"server_id":"s1"}}`,
			args: `{"operation":"sessions","server_id":"s1","datasource":"p"}`,
			want: base + "/instances/s1/sessions?datasource=p",
		},
		{
			name: "instance wait events sends its own default and the log source", tool: GetDbo11yInstanceDetail,
			body: `{"status":"success","data":{"server_id":"s1","wait_events":[]}}`,
			args: `{"operation":"wait_events","server_id":"s1","loki":"l"}`,
			want: base + "/instances/s1/wait-events?limit=10&loki=l",
		},
		{
			name: "top queries", tool: ListDbo11yTopQueries, body: `{"status":"success","data":{"server_id":"s1","queries":[]}}`,
			args: `{"server_id":"s1","limit":3,"datasource":"p","loki":"l"}`,
			want: base + "/instances/s1/queries?datasource=p&limit=3&loki=l",
		},
		{
			name: "query samples escape a server_id and query_id that hold a slash or question mark", tool: GetDbo11yQueryDetail,
			body: `{"status":"success","data":{"server_id":"s1","samples":[]}}`,
			args: `{"operation":"samples","server_id":"s/1","query_id":"q/1?x","database":"db","engine":"postgres","loki":"l","datasource":"p","step":"5m"}`,
			want: base + "/instances/s%2F1/queries/q%2F1%3Fx/samples?database=db&engine=postgres&limit=5&loki=l",
		},
		{
			name: "query timeline", tool: GetDbo11yQueryDetail, body: `{"status":"success","data":{"server_id":"s1","points":[]}}`,
			args: `{"operation":"timeline","server_id":"s1","query_id":"abc","step":"5m","datasource":"p","loki":"l","limit":9}`,
			want: base + "/instances/s1/queries/abc/timeline?datasource=p&step=5m",
		},
		{
			name: "recommendations", tool: ListDbo11yRecommendations, body: recsBody,
			args: `{"status":["firing"],"type":["connection_saturation","query_latency_spike"],"include":["content","next_steps"],"limit":2}`,
			want: base + "/recommendations?include=content%2Crequests&limit=2&status=firing&type=connection_saturation%2Cquery_latency_spike",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got struct{ method, uri, auth string }
			server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
				got.method, got.uri, got.auth = r.Method, r.RequestURI, r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(tc.body))
				assert.NoError(t, err)
			})
			defer server.Close()

			result, err := callDbo11yTool(ctx, tc.tool, tc.args)
			require.NoError(t, err)
			require.False(t, result.IsError, dbo11yResultText(t, result))
			assert.Equal(t, http.MethodGet, got.method)
			assert.Equal(t, tc.want, got.uri)
			assert.Equal(t, "Bearer test-api-key", got.auth, "the caller's credentials must reach the API")
		})
	}
}

func TestDbo11yTimeRange(t *testing.T) {
	t.Run("a relative range is resolved to milliseconds the API accepts", func(t *testing.T) {
		var from, to string
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			from, to = r.URL.Query().Get("from"), r.URL.Query().Get("to")
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"s1","queries":[]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		before := time.Now()
		_, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "s1", From: "now-1h", To: "now"})
		require.NoError(t, err)
		after := time.Now()

		fromMs, err := strconv.ParseInt(from, 10, 64)
		require.NoError(t, err, "from must be all digits: %q", from)
		toMs, err := strconv.ParseInt(to, 10, 64)
		require.NoError(t, err, "to must be all digits: %q", to)
		assert.GreaterOrEqual(t, toMs, before.UnixMilli())
		assert.LessOrEqual(t, toMs, after.UnixMilli())
		assert.InDelta(t, time.Hour.Milliseconds(), toMs-fromMs, 1000)
	})

	t.Run("RFC3339 and Unix milliseconds are sent as given", func(t *testing.T) {
		var from, to string
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			from, to = r.URL.Query().Get("from"), r.URL.Query().Get("to")
			_, err := w.Write([]byte(`{"status":"success","data":{"server_id":"s1","queries":[]}}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "s1", From: "2026-08-19T09:00:00.5+02:00", To: "1755594300000"})
		require.NoError(t, err)
		assert.Equal(t, "2026-08-19T09:00:00.5+02:00", from)
		assert.Equal(t, "1755594300000", to)
	})

	t.Run("an unparseable time is rejected without calling the server", func(t *testing.T) {
		ctx := dbo11yNeverCalledContext(t)
		_, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{From: "yesterday-ish"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid from")
		_, err = getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "s1", To: "soon"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid to")
		_, err = getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "schema", ServerID: "s1", QueryID: "q", From: "never"})
		require.Error(t, err)
		_, err = listDbo11yRecommendations(ctx, ListDbo11yRecommendationsParams{To: "never"})
		require.Error(t, err)
	})
}

func TestDbo11yInstanceListLimit(t *testing.T) {
	serve := func(t *testing.T, n int) context.Context {
		t.Helper()
		var body strings.Builder
		body.WriteString(`{"status":"success","data":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				body.WriteString(",")
			}
			fmt.Fprintf(&body, `{"server_id":"srv-%d","engine":"mysql"}`, i)
		}
		body.WriteString(`]}`)
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte(body.String()))
			assert.NoError(t, err)
		})
		t.Cleanup(server.Close)
		return ctx
	}

	for _, tc := range []struct {
		name      string
		fleet     int
		limit     int
		returned  int
		truncated bool
	}{
		{"a small fleet comes back whole", 7, 0, 7, false},
		{"the default cuts a large fleet", 120, 0, 50, true},
		{"a negative limit is the default", 120, -3, 50, true},
		{"a larger limit overrides the default", 120, 200, 120, false},
		{"a limit equal to the fleet is not truncated", 50, 50, 50, false},
		{"a small limit overrides the default", 120, 5, 5, true},
		{"one over the default limit is truncated", 51, 0, 50, true},
		{"the limit is capped", 600, 5000, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := listDbo11yInstances(serve(t, tc.fleet), ListDbo11yInstancesParams{Limit: tc.limit})
			require.NoError(t, err)
			assert.Equal(t, tc.fleet, result.Total, "total counts every match")
			assert.Equal(t, tc.returned, result.Returned)
			assert.Len(t, result.Instances, tc.returned)
			assert.Equal(t, tc.truncated, result.Truncated)
			assert.Equal(t, "srv-0", result.Instances[0].ServerID, "the API's order is kept")
		})
	}

	t.Run("an empty fleet is an empty list, not null", func(t *testing.T) {
		result, err := callDbo11yTool(serve(t, 0), ListDbo11yInstances, `{}`)
		require.NoError(t, err)
		text := dbo11yResultText(t, result)
		assert.Contains(t, text, `"instances":[]`)
		assert.Contains(t, text, `"total":0`)
		assert.Contains(t, text, `"truncated":false`)
	})

	t.Run("the limit argument is read from JSON", func(t *testing.T) {
		result, err := callDbo11yTool(serve(t, 20), ListDbo11yInstances, `{"limit":3}`)
		require.NoError(t, err)
		text := dbo11yResultText(t, result)
		assert.Contains(t, text, `"returned":3`)
		assert.Contains(t, text, `"truncated":true`)
		assert.Contains(t, text, `"total":20`)
	})
}

func TestDbo11yInputValidation(t *testing.T) {
	t.Run("label names the API would drop or reinterpret are rejected", func(t *testing.T) {
		ctx := dbo11yNeverCalledContext(t)
		for _, name := range []string{"__name__", "__x", "9lives", "a-b", "a.b", "team name", "équipe"} {
			_, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{Labels: map[string]string{name: "x"}})
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), "not a valid label name", name)
		}
	})

	t.Run("a pair with an empty value is ignored whatever its name", func(t *testing.T) {
		var query string
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			_, err := w.Write([]byte(`{"status":"success","data":[]}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{Labels: map[string]string{"from": "", "__name__": "", "bad name": "", "_ok_1": "v"}})
		require.NoError(t, err)
		assert.Equal(t, "_ok_1=v", query)
	})

	t.Run("a typed filter wins over the same label in the map", func(t *testing.T) {
		var query url.Values
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			_, err := w.Write([]byte(`{"status":"success","data":[]}`))
			assert.NoError(t, err)
		})
		defer server.Close()

		_, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{Engine: "postgres", Labels: map[string]string{"engine": "mysql"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"postgres"}, query["engine"])
	})

	t.Run("an empty identifier is rejected before any request", func(t *testing.T) {
		ctx := dbo11yNeverCalledContext(t)
		for _, tc := range []struct {
			name string
			call func() error
			want string
		}{
			{"get instance", func() error { _, err := getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: " "}); return err }, "server_id is required"},
			{"instance detail", func() error {
				_, err := getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "sessions"})
				return err
			}, "server_id is required"},
			{"top queries", func() error { _, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{}); return err }, "server_id is required"},
			{"query detail without a server_id", func() error {
				_, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "schema", QueryID: "q"})
				return err
			}, "server_id is required"},
			{"query detail without a query_id", func() error {
				_, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "schema", ServerID: "s"})
				return err
			}, "query_id is required"},
		} {
			err := tc.call()
			require.Error(t, err, tc.name)
			assert.Contains(t, err.Error(), tc.want, tc.name)
		}
	})
}

func TestDbo11yErrorHints(t *testing.T) {
	respond := func(t *testing.T, status int, contentType, body string) error {
		t.Helper()
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(status)
			_, err := w.Write([]byte(body))
			assert.NoError(t, err)
		})
		defer server.Close()
		_, err := getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "s1"})
		require.Error(t, err)
		return err
	}
	apiError := func(code, message string) string {
		return `{"status":"error","error":{"code":"` + code + `","message":"` + message + `"}}`
	}

	t.Run("a response that is not the error envelope keeps its status and body", func(t *testing.T) {
		err := respond(t, http.StatusNotFound, "text/plain", "404 page not found")
		assert.Equal(t, "request failed with status 404: 404 page not found", err.Error())
	})

	wantHint := map[string]string{
		"upstream":               "A data source query failed",
		"instance_not_found":     "check the server_id against the instance listing",
		"explain_plan_not_found": "A wider from looks further back",
		"query_tables_not_found": "No table association was recorded",
	}
	for code, status := range map[string]int{
		"upstream": http.StatusBadGateway, "instance_not_found": http.StatusNotFound,
		"explain_plan_not_found": http.StatusNotFound, "query_tables_not_found": http.StatusNotFound,
	} {
		t.Run(code+" gets a hint after the API's own message", func(t *testing.T) {
			err := respond(t, status, "application/json", apiError(code, "the api's message"))
			assert.Contains(t, err.Error(), "dbo11y "+code+": the api's message (status "+strconv.Itoa(status)+"). ")
			assert.Contains(t, err.Error(), wantHint[code])
		})
	}

	t.Run("a JSON error without a code is reported as the raw body", func(t *testing.T) {
		err := respond(t, http.StatusBadRequest, "application/json", `{"status":"error","error":{"message":"no code here"}}`)
		assert.Equal(t, `request failed with status 400: {"status":"error","error":{"message":"no code here"}}`, err.Error())
	})

	t.Run("a code without a hint is the API's message alone", func(t *testing.T) {
		err := respond(t, http.StatusBadRequest, "application/json", apiError("invalid_limit", "limit must be an integer between 1 and 100"))
		assert.Equal(t, "dbo11y invalid_limit: limit must be an integer between 1 and 100 (status 400)", err.Error())
	})

	t.Run("a response over the size cap is refused", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write(make([]byte, defaultResponseLimitBytes+1))
			assert.NoError(t, err)
		})
		defer server.Close()
		_, err := getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "s1"})
		require.ErrorIs(t, err, errResponseBodyTooLarge)
	})

	t.Run("a body that is not the envelope is a decode error", func(t *testing.T) {
		server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte(`<html>`))
			assert.NoError(t, err)
		})
		defer server.Close()
		_, err := getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "s1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode response")
	})
}

func TestDbo11yClientTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  time.Duration
		want time.Duration
	}{
		{name: "unset keeps the 30s default", cfg: 0, want: 30 * time.Second},
		{name: "longer --grafana-timeout is used", cfg: 300 * time.Second, want: 300 * time.Second},
		{name: "shorter --grafana-timeout is used", cfg: 10 * time.Second, want: 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
				URL:     "http://grafana.example",
				Timeout: tc.cfg,
			})
			client, err := newDbo11yClient(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.want, client.httpClient.Timeout)
		})
	}
}

func TestDbo11yToolSchemas(t *testing.T) {
	tools := []mcpgrafana.Tool{
		ListDbo11yInstances, GetDbo11yInstance, GetDbo11yInstanceDetail,
		ListDbo11yTopQueries, GetDbo11yQueryDetail, ListDbo11yRecommendations,
	}
	schemas := map[string]string{}
	for _, tool := range tools {
		raw, err := json.Marshal(tool.Tool.InputSchema)
		require.NoError(t, err)
		schemas[tool.Tool.Name] = string(raw)

		// A stray escape in a jsonschema tag survives into the text a model reads.
		assert.NotContains(t, string(raw), `\\`, "%s: a description keeps a backslash", tool.Tool.Name)
		assert.NotContains(t, string(raw), "plugin", "%s: user-facing text names the API, not its plugin", tool.Tool.Name)
		assert.NotContains(t, tool.Tool.Description, "plugin", tool.Tool.Name)
		assert.NotContains(t, strings.ToLower(tool.Tool.Description), "utilisation", tool.Tool.Name)
	}

	assert.Contains(t, schemas["get_dbo11y_query_detail"], `"timeline"`)
	assert.Contains(t, schemas["list_dbo11y_recommendations"], `"last_seen"`)
	assert.NotContains(t, schemas["get_dbo11y_instance"], `"from"`, "the instance rollup has no start time")
	assert.Contains(t, schemas["get_dbo11y_instance"], `"to"`)
	assert.Contains(t, schemas["list_dbo11y_instances"], `"limit"`)

	// The API answers a limit over 10 with include by rejecting it, so the
	// descriptions must not promise that it is lowered.
	assert.Contains(t, schemas["list_dbo11y_recommendations"], "rejected")
	assert.NotContains(t, schemas["list_dbo11y_recommendations"], "caps limit")

	// The top queries carry the engine, and a caller that drops it can't look up
	// a Postgres query_id of sixteen digits.
	assert.Contains(t, ListDbo11yTopQueries.Tool.Description, "pass the returned engine along")
	assert.Contains(t, schemas["get_dbo11y_query_detail"], "sixteen digits")
}
