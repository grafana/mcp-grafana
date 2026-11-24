//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the contract a client sees: the arguments a tool accepts, the
// request each one becomes, and the JSON a result is written as.

const dbo11yTestBase = "/api/plugins/grafana-dbo11y-app/resources/api/v1"

// serveDbo11y answers every request with body and records the last request URI.
func serveDbo11y(t *testing.T, body string) (context.Context, *string) {
	t.Helper()
	var uri string
	server, ctx := setupMockDbo11yServer(func(w http.ResponseWriter, r *http.Request) {
		uri = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(body))
		assert.NoError(t, err)
	})
	t.Cleanup(server.Close)
	return ctx, &uri
}

// inputSchema returns a tool's input properties and required names.
func inputSchema(t *testing.T, tool mcpgrafana.Tool) (map[string]map[string]any, []string) {
	t.Helper()
	raw, err := json.Marshal(tool.Tool.InputSchema)
	require.NoError(t, err)
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	return schema.Properties, schema.Required
}

func enumOf(t *testing.T, props map[string]map[string]any, name string) []string {
	t.Helper()
	values, ok := props[name]["enum"].([]any)
	require.True(t, ok, "%s has no enum", name)
	var out []string
	for _, v := range values {
		out = append(out, v.(string))
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestDbo11yToolInputs(t *testing.T) {
	for _, tc := range []struct {
		tool       mcpgrafana.Tool
		properties []string
		required   []string
	}{
		{ListDbo11yInstances, []string{"datasource", "db_instance_identifier", "engine", "engine_version", "from", "instance", "labels", "limit", "provider_account", "provider_name", "provider_region", "server_id", "to"}, nil},
		{GetDbo11yInstance, []string{"datasource", "server_id", "to"}, []string{"server_id"}},
		{GetDbo11yInstanceDetail, []string{"datasource", "from", "limit", "loki", "operation", "server_id", "to"}, []string{"operation", "server_id"}},
		{ListDbo11yTopQueries, []string{"datasource", "from", "limit", "loki", "server_id", "to"}, []string{"server_id"}},
		{GetDbo11yQueryDetail, []string{"database", "datasource", "engine", "from", "limit", "loki", "operation", "query_id", "server_id", "step", "to"}, []string{"operation", "query_id", "server_id"}},
		{ListDbo11yRecommendations, []string{"category", "database", "datasource", "db_instance_identifier", "env", "from", "include", "instance", "limit", "query_id", "scope", "server_id", "service", "severity", "site", "sort", "status", "to", "type"}, nil},
	} {
		t.Run(tc.tool.Tool.Name, func(t *testing.T) {
			props, required := inputSchema(t, tc.tool)
			assert.Equal(t, tc.properties, sortedKeys(props))
			sort.Strings(required)
			assert.Equal(t, tc.required, required)
		})
	}

	t.Run("enums", func(t *testing.T) {
		detail, _ := inputSchema(t, GetDbo11yInstanceDetail)
		assert.Equal(t, []string{"sessions", "wait_events"}, enumOf(t, detail, "operation"))
		query, _ := inputSchema(t, GetDbo11yQueryDetail)
		assert.Equal(t, []string{"explain_plan", "samples", "schema", "timeline", "wait_events"}, enumOf(t, query, "operation"))
		assert.Equal(t, []string{"mysql", "postgres"}, enumOf(t, query, "engine"), "only the engines the API documents")
		recs, _ := inputSchema(t, ListDbo11yRecommendations)
		assert.Equal(t, []string{"first_seen", "last_seen", "severity", "type"}, enumOf(t, recs, "sort"))
	})
}

func TestDbo11yFollowUpMapping(t *testing.T) {
	// What the API sends for each operation, from its follow-up request builder.
	instanceParams := map[string]string{"from": "2026-08-19T08:45:00Z", "to": "2026-08-19T10:00:00Z", "datasource": "p"}
	queryParams := map[string]string{"from": "2026-08-19T08:45:00Z", "to": "2026-08-19T10:00:00Z", "engine": "postgres", "database": "shop"}
	with := func(base map[string]string, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	for _, tc := range []struct {
		operationID string
		params      map[string]string
		tool        string
		operation   string
	}{
		{"getInstanceSessions", instanceParams, "get_dbo11y_instance_detail", "sessions"},
		{"getInstanceQueries", with(instanceParams, map[string]string{"limit": "10"}), "list_dbo11y_top_queries", ""},
		{"getInstanceWaitEvents", with(instanceParams, map[string]string{"limit": "10"}), "get_dbo11y_instance_detail", "wait_events"},
		{"getQueryTimeline", with(queryParams, map[string]string{"datasource": "p", "step": "300s"}), "get_dbo11y_query_detail", "timeline"},
		{"getExplainPlan", queryParams, "get_dbo11y_query_detail", "explain_plan"},
		{"getWaitEvents", queryParams, "get_dbo11y_query_detail", "wait_events"},
		{"getQuerySchema", queryParams, "get_dbo11y_query_detail", "schema"},
		{"getQuerySamples", with(queryParams, map[string]string{"limit": "10"}), "get_dbo11y_query_detail", "samples"},
	} {
		t.Run(tc.operationID, func(t *testing.T) {
			rec := Dbo11yRecommendation{
				AffectedEntities: []Dbo11yRecommendationEntity{{ServerID: "srv-1"}},
				Query: &struct {
					QueryID string `json:"query_id"`
				}{QueryID: "-4023954832"},
				Requests: []dbo11yFollowUpRequest{{OperationID: tc.operationID, Params: tc.params, Description: "d"}},
			}
			steps := rec.nextSteps()
			require.Len(t, steps, 1)
			step := steps[0]
			assert.Equal(t, tc.tool, step.Tool)
			assert.Equal(t, "d", step.Description)
			if tc.operation == "" {
				assert.NotContains(t, step.Arguments, "operation")
			} else {
				assert.Equal(t, tc.operation, step.Arguments["operation"])
			}

			// The step must be a call the named tool accepts as it stands.
			var tool mcpgrafana.Tool
			switch tc.tool {
			case "get_dbo11y_instance_detail":
				tool = GetDbo11yInstanceDetail
			case "list_dbo11y_top_queries":
				tool = ListDbo11yTopQueries
			case "get_dbo11y_query_detail":
				tool = GetDbo11yQueryDetail
			}
			require.Equal(t, tc.tool, tool.Tool.Name)
			props, required := inputSchema(t, tool)
			for name := range step.Arguments {
				assert.Contains(t, props, name, "%s has no argument %q", tc.tool, name)
			}
			for _, name := range required {
				assert.Contains(t, step.Arguments, name, "%s needs %q", tc.tool, name)
			}
			if tc.operation != "" {
				assert.Contains(t, enumOf(t, props, "operation"), tc.operation)
			}
			if database, ok := step.Arguments["database"]; ok {
				assert.Equal(t, "shop", database)
			}
		})
	}

	t.Run("only the first affected entity is used", func(t *testing.T) {
		rec := Dbo11yRecommendation{
			AffectedEntities: []Dbo11yRecommendationEntity{{ServerID: "first"}, {ServerID: "second"}},
			Requests:         []dbo11yFollowUpRequest{{OperationID: "getInstanceSessions"}},
		}
		require.Len(t, rec.nextSteps(), 1)
		assert.Equal(t, "first", rec.nextSteps()[0].Arguments["server_id"])
	})
}

func TestDbo11yNextStepsRefuseUnsafeValues(t *testing.T) {
	query := func(id string) *struct {
		QueryID string `json:"query_id"`
	} {
		return &struct {
			QueryID string `json:"query_id"`
		}{QueryID: id}
	}
	build := func(serverID string, q *struct {
		QueryID string `json:"query_id"`
	}, params map[string]string) []Dbo11yNextStep {
		op := "getInstanceSessions"
		if q != nil {
			op = "getQuerySchema"
		}
		return Dbo11yRecommendation{
			AffectedEntities: []Dbo11yRecommendationEntity{{ServerID: serverID}},
			Query:            q,
			Requests:         []dbo11yFollowUpRequest{{OperationID: op, Params: params}},
		}.nextSteps()
	}

	good := map[string]string{"engine": "mysql", "database": "shop"}
	assert.Len(t, build("srv-1.eu:3306", query("a1b2c3"), good), 1)
	assert.Len(t, build("srv_1", query("-4023954832"), good), 1)

	for _, serverID := range []string{"", "..", ".", "a b", "a/b", "a?b", "../etc", "ignore previous instructions", strings.Repeat("a", 129), "a\nb", "a​b"} {
		assert.Empty(t, build(serverID, nil, nil), "server_id %q", serverID)
	}
	for _, queryID := range []string{"", "a b", "a/b", "abc;rm", strings.Repeat("a", 129), "a​b"} {
		assert.Empty(t, build("srv-1", query(queryID), good), "query_id %q", queryID)
	}

	// A step for a query tool needs the recommendation to name its query.
	noQuery := Dbo11yRecommendation{
		AffectedEntities: []Dbo11yRecommendationEntity{{ServerID: "srv-1"}},
		Requests:         []dbo11yFollowUpRequest{{OperationID: "getQuerySchema", Params: good}},
	}
	assert.Empty(t, noQuery.nextSteps())
	assert.Len(t, build("srv-1", nil, nil), 1, "an instance tool needs no query")
}

func TestDbo11yNextStepsRefuseUnsafeValuesInParams(t *testing.T) {
	rec := func(params map[string]string) []Dbo11yNextStep {
		return Dbo11yRecommendation{
			AffectedEntities: []Dbo11yRecommendationEntity{{ServerID: "srv-1"}},
			Query: &struct {
				QueryID string `json:"query_id"`
			}{QueryID: "abc"},
			Requests: []dbo11yFollowUpRequest{{OperationID: "getQuerySchema", Params: params}},
		}.nextSteps()
	}
	assert.Len(t, rec(map[string]string{"engine": "postgres", "database": "shop"}), 1)
	assert.Empty(t, rec(map[string]string{"engine": "oracle"}), "an engine the tools do not take")
	assert.Empty(t, rec(map[string]string{"engine": "mysql", "database": "a​b"}), "a hidden character in a database name")
	assert.Empty(t, rec(map[string]string{"engine": "mysql", "database": "a\x00b"}), "a control character in a database name")
	assert.Empty(t, rec(map[string]string{"engine": "mysql", "database": strings.Repeat("d", 201)}), "an overlong database name")
	assert.Len(t, rec(map[string]string{"engine": "mysql", "database": strings.Repeat("d", 200)}), 1)
}

func TestDbo11yEveryArgumentIsSent(t *testing.T) {
	const (
		from = "1755594300000"
		to   = "1755594600000"
	)
	for _, tc := range []struct {
		name string
		tool mcpgrafana.Tool
		args string
		body string
		path string
		want url.Values
	}{
		{
			name: "list instances", tool: ListDbo11yInstances, body: `{"data":[]}`, path: "/instances",
			args: `{"server_id":"a","instance":"b","engine":"c","engine_version":"d","db_instance_identifier":"e","provider_name":"f","provider_account":"g","provider_region":"h","labels":{"team":"t"},"limit":5,"datasource":"p","from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"server_id": {"a"}, "instance": {"b"}, "engine": {"c"}, "engine_version": {"d"}, "db_instance_identifier": {"e"}, "provider_name": {"f"}, "provider_account": {"g"}, "provider_region": {"h"}, "team": {"t"}, "datasource": {"p"}, "from": {from}, "to": {to}},
		},
		{
			name: "get instance", tool: GetDbo11yInstance, body: `{"data":{}}`, path: "/instances/a%2Fb%3Fc",
			args: `{"server_id":"a/b?c","datasource":"p","to":"` + to + `"}`,
			want: url.Values{"datasource": {"p"}, "to": {to}},
		},
		{
			name: "instance sessions never sends the log source or a limit", tool: GetDbo11yInstanceDetail, body: `{"data":{}}`, path: "/instances/a%2Fb%3Fc/sessions",
			args: `{"operation":"sessions","server_id":"a/b?c","datasource":"p","loki":"l","limit":3,"from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"datasource": {"p"}, "from": {from}, "to": {to}},
		},
		{
			name: "instance wait events", tool: GetDbo11yInstanceDetail, body: `{"data":{"wait_events":[]}}`, path: "/instances/a%2Fb%3Fc/wait-events",
			args: `{"operation":"wait_events","server_id":"a/b?c","datasource":"p","loki":"l","limit":3,"from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"datasource": {"p"}, "loki": {"l"}, "limit": {"3"}, "from": {from}, "to": {to}},
		},
		{
			name: "top queries", tool: ListDbo11yTopQueries, body: `{"data":{"queries":[]}}`, path: "/instances/a%2Fb%3Fc/queries",
			args: `{"server_id":"a/b?c","limit":3,"datasource":"p","loki":"l","from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"limit": {"3"}, "datasource": {"p"}, "loki": {"l"}, "from": {from}, "to": {to}},
		},
		{
			name: "query samples", tool: GetDbo11yQueryDetail, body: `{"data":{"samples":[]}}`, path: "/instances/a%2Fb%3Fc/queries/q%2F1%3Fx/samples",
			args: `{"operation":"samples","server_id":"a/b?c","query_id":"q/1?x","database":"db","engine":"postgres","limit":3,"loki":"l","datasource":"p","step":"5m","from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"database": {"db"}, "engine": {"postgres"}, "limit": {"3"}, "loki": {"l"}, "from": {from}, "to": {to}},
		},
		{
			name: "query explain plan takes no limit", tool: GetDbo11yQueryDetail, body: `{"data":{}}`, path: "/instances/s/queries/q/explain-plan",
			args: `{"operation":"explain_plan","server_id":"s","query_id":"q","database":"db","engine":"mysql","limit":3,"loki":"l","datasource":"p","step":"5m","from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"database": {"db"}, "engine": {"mysql"}, "loki": {"l"}, "from": {from}, "to": {to}},
		},
		{
			name: "query wait events takes no limit", tool: GetDbo11yQueryDetail, body: `{"data":{"wait_events":[]}}`, path: "/instances/s/queries/q/wait-events",
			args: `{"operation":"wait_events","server_id":"s","query_id":"q","database":"db","limit":3,"loki":"l","datasource":"p","from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"database": {"db"}, "loki": {"l"}, "from": {from}, "to": {to}},
		},
		{
			name: "query schema takes no limit", tool: GetDbo11yQueryDetail, body: `{"data":{"tables":[]}}`, path: "/instances/s/queries/q/schema",
			args: `{"operation":"schema","server_id":"s","query_id":"q","limit":3,"loki":"l","datasource":"p","step":"5m"}`,
			want: url.Values{"loki": {"l"}},
		},
		{
			name: "query timeline", tool: GetDbo11yQueryDetail, body: `{"data":{"points":[]}}`, path: "/instances/s/queries/q/timeline",
			args: `{"operation":"timeline","server_id":"s","query_id":"q","database":"db","engine":"mysql","limit":3,"loki":"l","datasource":"p","step":"5m","from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"database": {"db"}, "engine": {"mysql"}, "datasource": {"p"}, "step": {"5m"}, "from": {from}, "to": {to}},
		},
		{
			name: "recommendations", tool: ListDbo11yRecommendations, body: `{"data":{"recommendations":[]}}`, path: "/recommendations",
			args: `{"status":["firing","resolved"],"severity":["critical"],"category":["resource"],"type":["connection_saturation"],"scope":["instance"],"server_id":"a","service":"b","env":"c","site":"d","instance":"e","db_instance_identifier":"f","database":"g","query_id":"h","sort":"last_seen","include":["content","queries"],"limit":4,"datasource":"p","from":"` + from + `","to":"` + to + `"}`,
			want: url.Values{"status": {"firing,resolved"}, "severity": {"critical"}, "category": {"resource"}, "type": {"connection_saturation"}, "scope": {"instance"}, "server_id": {"a"}, "service": {"b"}, "env": {"c"}, "site": {"d"}, "instance": {"e"}, "db_instance_identifier": {"f"}, "database": {"g"}, "query_id": {"h"}, "sort": {"last_seen"}, "include": {"content,queries"}, "limit": {"4"}, "datasource": {"p"}, "from": {from}, "to": {to}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, uri := serveDbo11y(t, `{"status":"success",`+strings.TrimPrefix(tc.body, "{"))
			result, err := callDbo11yTool(ctx, tc.tool, tc.args)
			require.NoError(t, err)
			require.False(t, result.IsError, dbo11yResultText(t, result))

			// The instance listing's limit is applied by the tool and never sent.
			assert.Equal(t, dbo11yTestBase+tc.path+"?"+tc.want.Encode(), *uri)
		})
	}
}

func TestDbo11yDefaultLimitsAreSentAndNegativeOnesReplaced(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tool  mcpgrafana.Tool
		args  string
		body  string
		limit string
	}{
		{"top queries", ListDbo11yTopQueries, `{"server_id":"s","limit":-1}`, `{"data":{"queries":[]}}`, "10"},
		{"top queries default", ListDbo11yTopQueries, `{"server_id":"s"}`, `{"data":{"queries":[]}}`, "10"},
		{"samples", GetDbo11yQueryDetail, `{"operation":"samples","server_id":"s","query_id":"q","limit":-1}`, `{"data":{"samples":[]}}`, "5"},
		{"samples default", GetDbo11yQueryDetail, `{"operation":"samples","server_id":"s","query_id":"q"}`, `{"data":{"samples":[]}}`, "5"},
		{"instance wait events", GetDbo11yInstanceDetail, `{"operation":"wait_events","server_id":"s","limit":-1}`, `{"data":{"wait_events":[]}}`, "10"},
		{"instance wait events default", GetDbo11yInstanceDetail, `{"operation":"wait_events","server_id":"s"}`, `{"data":{"wait_events":[]}}`, "10"},
		{"recommendations", ListDbo11yRecommendations, `{"limit":-1}`, `{"data":{"recommendations":[]}}`, "10"},
		{"recommendations default", ListDbo11yRecommendations, `{}`, `{"data":{"recommendations":[]}}`, "10"},
		{"recommendations explicit", ListDbo11yRecommendations, `{"limit":25}`, `{"data":{"recommendations":[]}}`, "25"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, uri := serveDbo11y(t, `{"status":"success",`+strings.TrimPrefix(tc.body, "{"))
			result, err := callDbo11yTool(ctx, tc.tool, tc.args)
			require.NoError(t, err)
			require.False(t, result.IsError, dbo11yResultText(t, result))
			parsed, err := url.Parse(*uri)
			require.NoError(t, err)
			assert.Equal(t, tc.limit, parsed.Query().Get("limit"))
		})
	}
}

func TestDbo11yTimeParsing(t *testing.T) {
	sent := func(t *testing.T, from, to string) (string, string) {
		t.Helper()
		ctx, uri := serveDbo11y(t, `{"status":"success","data":{"queries":[]}}`)
		_, err := listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "s", From: from, To: to})
		require.NoError(t, err)
		parsed, err := url.Parse(*uri)
		require.NoError(t, err)
		return parsed.Query().Get("from"), parsed.Query().Get("to")
	}

	t.Run("Unix seconds are scaled to the milliseconds the API reads", func(t *testing.T) {
		from, to := sent(t, "1755594300", "1755594600")
		assert.Equal(t, "1755594300000", from)
		assert.Equal(t, "1755594600000", to)
	})

	t.Run("the seconds boundary is eleven digits", func(t *testing.T) {
		from, to := sent(t, "99999999999", "100000000000")
		assert.Equal(t, "99999999999000", from, "eleven digits can only be seconds")
		assert.Equal(t, "100000000000", to, "twelve digits are milliseconds")
	})

	t.Run("Unix milliseconds are sent as given", func(t *testing.T) {
		from, to := sent(t, "1755594300000", "1755594600123")
		assert.Equal(t, "1755594300000", from)
		assert.Equal(t, "1755594600123", to)
	})

	t.Run("a date alone is the start of the day for from and the end of it for to", func(t *testing.T) {
		from, to := sent(t, "2026-08-19", "2026-08-19")
		day := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, strconv.FormatInt(day.UnixMilli(), 10), from)
		assert.Equal(t, strconv.FormatInt(day.AddDate(0, 0, 1).Add(-time.Millisecond).UnixMilli(), 10), to)
	})

	t.Run("surrounding space is trimmed", func(t *testing.T) {
		from, to := sent(t, " 1755594300000 ", "\t2026-08-19T10:00:00Z\n")
		assert.Equal(t, "1755594300000", from)
		assert.Equal(t, "2026-08-19T10:00:00Z", to)
	})

	t.Run("the other tools reject an unparseable time without a request", func(t *testing.T) {
		ctx := dbo11yNeverCalledContext(t)
		_, err := getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "sessions", ServerID: "s", From: "never"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid from")
		_, err = listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "s", To: "never"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid to")
	})
}

func TestDbo11yEmptyFleetFromANullBody(t *testing.T) {
	ctx, _ := serveDbo11y(t, `{"status":"success","data":null}`)
	result, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{})
	require.NoError(t, err)
	assert.NotNil(t, result.Instances, "a null list becomes an empty one")
	assert.Empty(t, result.Instances)
	out, err := json.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, `{"total":0,"returned":0,"truncated":false,"instances":[]}`, string(out))
}

// roundTrip serves data as an API response, calls fn, and requires the result
// to be written back out as the same JSON: every field the API sends is kept
// under the name it arrived with.
func roundTrip(t *testing.T, data string, fn func(ctx context.Context) (any, error)) {
	t.Helper()
	ctx, _ := serveDbo11y(t, `{"status":"success","data":`+data+`}`)
	result, err := fn(ctx)
	require.NoError(t, err)
	out, err := json.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, data, string(out))
}

func TestDbo11yResponsesKeepEveryField(t *testing.T) {
	t.Run("instances", func(t *testing.T) {
		list := `[{"server_id":"srv-1","instance":"db:3306","engine":"mysql","engine_version":"8.0.35",
			"cloud_provider":{
				"aws":{"db_instance_id":"i-1","account_id":"123","region":"eu-west-1"},
				"azure":{"db_instance_id":"i-2","subscription_id":"sub","resource_group":"rg"},
				"gcp":{"db_instance_id":"i-3","project_id":"proj","region":"europe-west1"}},
			"labels":{"team":"payments"},
			"metrics":{"qps":1.5,"avg_latency_ms":2.5,"error_rate":0,"cpu_percent":33.5}}]`
		ctx, _ := serveDbo11y(t, `{"status":"success","data":`+list+`}`)
		result, err := listDbo11yInstances(ctx, ListDbo11yInstancesParams{})
		require.NoError(t, err)
		out, err := json.Marshal(result)
		require.NoError(t, err)
		assert.JSONEq(t, `{"total":1,"returned":1,"truncated":false,"instances":`+list+`}`, string(out))
	})

	t.Run("one instance", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","instance":"db:5432","engine":"postgres","engine_version":"16.2","labels":{"a":"b"},
			"cloud_provider":{"aws":{"db_instance_id":"i-1","account_id":"123","region":"eu-west-1"}},
			"metrics":{"qps":1,"avg_latency_ms":2,"error_rate":0.5,"cpu_percent":10}}`,
			func(ctx context.Context) (any, error) {
				return getDbo11yInstance(ctx, GetDbo11yInstanceParams{ServerID: "srv-1"})
			})
	})

	t.Run("sessions", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","engine":"postgres","total":12,"max_connections":100,"by_state":{"active":3,"idle":9},"longest_transaction_seconds":42.5}`,
			func(ctx context.Context) (any, error) {
				return getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "sessions", ServerID: "srv-1"})
			})
	})

	t.Run("instance wait events", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","wait_events":[{"type":"IO Wait","event":"DataFileRead","wait_seconds":12.5,"occurrences":300,"waiting_sessions":2}],"total_wait_seconds":12.5}`,
			func(ctx context.Context) (any, error) {
				return getDbo11yInstanceDetail(ctx, GetDbo11yInstanceDetailParams{Operation: "wait_events", ServerID: "srv-1"})
			})
	})

	t.Run("top queries", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","engine":"mysql","queries":[
			{"digest":"abc","query_id":"-1","database":"shop","query_text":"SELECT ?","db_load":0.42,"calls":12500,"qps":3.5,"avg_latency_ms":4.5,"error_rate":0.01,"rows_examined":99}]}`,
			func(ctx context.Context) (any, error) {
				return listDbo11yTopQueries(ctx, ListDbo11yTopQueriesParams{ServerID: "srv-1"})
			})
	})

	t.Run("query samples", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","engine":"postgres","query_id":"-1","database":"shop","truncated":true,"samples":[
			{"timestamp":"2026-09-28T10:00:00Z","duration_seconds":1.5,"sql_text":"SELECT 1","database":"shop","traceparent":"00-abc-def-01","fields":{"rows":"3"}}]}`,
			func(ctx context.Context) (any, error) {
				return getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "samples", ServerID: "srv-1", QueryID: "-1"})
			})
	})

	t.Run("explain plan", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","engine":"mysql","query_id":"abc","timestamp":"2026-09-28T10:00:00Z","plan":{"query_block":{"select_id":1}}}`,
			func(ctx context.Context) (any, error) {
				return getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "explain_plan", ServerID: "srv-1", QueryID: "abc"})
			})
	})

	t.Run("query wait events", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","engine":"mysql","query_id":"abc","database":"shop","wait_events":[{"type":"IO Wait","wait_seconds":3.5}],"total_wait_seconds":3.5}`,
			func(ctx context.Context) (any, error) {
				return getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "wait_events", ServerID: "srv-1", QueryID: "abc"})
			})
	})

	t.Run("schema", func(t *testing.T) {
		roundTrip(t, `{"server_id":"srv-1","engine":"mysql","query_id":"abc","database":"shop","truncated":true,
			"tables":[{"table":"orders","database":"shop","spec":{"columns":[{"name":"id"}]},"create_ddl":"CREATE TABLE orders (id int)"}]}`,
			func(ctx context.Context) (any, error) {
				return getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "schema", ServerID: "srv-1", QueryID: "abc"})
			})
	})

	t.Run("timeline", func(t *testing.T) {
		ctx, _ := serveDbo11y(t, `{"status":"success","data":{"server_id":"srv-1","engine":"mysql","query_id":"abc","database":"shop","step_seconds":300,
			"points":[{"timestamp":1755594300,"calls":120,"avg_latency_ms":3.2,"error_rate":0}]}}`)
		result, err := getDbo11yQueryDetail(ctx, GetDbo11yQueryDetailParams{Operation: "timeline", ServerID: "srv-1", QueryID: "abc"})
		require.NoError(t, err)
		out, err := json.Marshal(result)
		require.NoError(t, err)
		assert.JSONEq(t, `{"server_id":"srv-1","engine":"mysql","query_id":"abc","database":"shop","step_seconds":300,
			"points":[{"window_end":"`+time.Unix(1755594300, 0).UTC().Format(time.RFC3339)+`","calls":120,"avg_latency_ms":3.2,"error_rate":0}]}`, string(out))
	})
}

func TestDbo11yRecommendationsGolden(t *testing.T) {
	const head = `{"window":{"from":"2026-08-19T08:00:00Z","to":"2026-08-19T10:00:00Z"},"total":2,"recommendations":[
	{"type":"query_latency_spike","category":"performance","severity":"warning","status":"firing","scope":"schema","engine":"postgres",
	 "affected_entities":[{"name":"shop-db","server_id":"srv-1","service":"shop-db","env":"111111111111","site":"eu-west-1","provider":"aws","instance":"shop-db:5432","db_instance_identifier":"shop-db-rds"}],
	 "database":"shop","query":{"query_id":"-4023954832"},
	 "streak_started_at":"2026-08-19T08:41:07Z","first_seen":"2026-08-19T09:00:12Z","last_seen":"2026-08-19T09:59:41Z","duration_seconds":4714,
	 "detection":{"family":"anomaly"},
	 "content":{"title":"Query latency spike","summary":"Query latency rose","details":"## Why","possible_causes":[{"title":"Missing index","body":"A scan."}],"learn_more":[{"label":"Docs","url":"https://example.com/docs"}]},
	 "queries":[{"name":"query_latency","title":"Latency","unit":"s","expr":"up","peak_expr":"max_over_time(up[5m])","peak_from":"2026-08-19T08:50:00Z",
	   "baseline":{"title":"Hour before","expr":"up offset 1h"},"note":"all schemas","datasource":{"type":"prometheus","uid":"grafanacloud-prom"},
	   "from":"2026-08-19T08:45:00Z","to":"2026-08-19T10:00:00Z","step_seconds":60}],
	 `
	const tail = `},
	{"type":"idle_connection","category":"resource","severity":"info","status":"resolved","scope":"instance",
	 "affected_entities":[{"name":"unknown"}],"first_seen":"2026-08-19T08:10:00Z","last_seen":"2026-08-19T08:20:00Z",
	 "detection":{"family":"saturation"},"content":{"title":"Idle connections","summary":"Idle"},"queries":[],`
	const requests = `"requests":[
	  {"operation_id":"getQueryTimeline","method":"GET","path":"/api/v1/instances/srv-1/queries/-4023954832/timeline","params":{"from":"2026-08-19T08:45:00Z","to":"2026-08-19T10:00:00Z","engine":"postgres","database":"shop","datasource":"p","step":"300s"},"description":"Timeline."},
	  {"operation_id":"getQuerySamples","method":"GET","path":"/x","params":{"from":"2026-08-19T08:45:00Z","to":"2026-08-19T10:00:00Z","engine":"postgres","database":"shop","limit":"10"},"description":"Samples."}]`
	const second = `"requests":[{"operation_id":"getInstanceSessions","method":"GET","path":"/x","params":{},"description":"Sessions."}]}]}`

	const nextSteps = `"next_steps":[
	  {"tool":"get_dbo11y_query_detail","arguments":{"operation":"timeline","server_id":"srv-1","query_id":"-4023954832","from":"2026-08-19T08:45:00Z","to":"2026-08-19T10:00:00Z","engine":"postgres","database":"shop","datasource":"p","step":"300s"},"description":"Timeline."},
	  {"tool":"get_dbo11y_query_detail","arguments":{"operation":"samples","server_id":"srv-1","query_id":"-4023954832","from":"2026-08-19T08:45:00Z","to":"2026-08-19T10:00:00Z","engine":"postgres","database":"shop","limit":10},"description":"Samples."}]`
	const secondNextSteps = `"next_steps":[]}]}`

	t.Run("everything asked for is written under the names a client reads", func(t *testing.T) {
		ctx, _ := serveDbo11y(t, `{"status":"success","data":`+head+requests+tail+second+`}`)
		result, err := callDbo11yTool(ctx, ListDbo11yRecommendations, `{"include":["content","queries","next_steps"]}`)
		require.NoError(t, err)
		assert.JSONEq(t, head+nextSteps+tail+secondNextSteps, dbo11yResultText(t, result))
	})

	t.Run("a field not asked for is absent and a field asked for with nothing to show is empty", func(t *testing.T) {
		// Without include the API sends neither field, and the tool adds neither.
		const bare = `{"window":{"from":"a","to":"b"},"total":1,"recommendations":[{"type":"idle_connection","category":"resource","severity":"info","status":"firing","scope":"instance",
			"affected_entities":[{"name":"db"}],"first_seen":"x","last_seen":"y","detection":{"family":"saturation"},"content":{"title":"t","summary":"s"}}]}`
		ctx, _ := serveDbo11y(t, `{"status":"success","data":`+bare+`}`)
		result, err := callDbo11yTool(ctx, ListDbo11yRecommendations, `{}`)
		require.NoError(t, err)
		text := dbo11yResultText(t, result)
		assert.JSONEq(t, bare, text)
		assert.NotContains(t, text, "next_steps")
		assert.NotContains(t, text, `"queries"`)
		assert.NotContains(t, text, `"requests"`)

		// Asked for next_steps, none can be written: the API sends requests: [].
		const asked = `{"window":{"from":"a","to":"b"},"total":1,"recommendations":[{"type":"idle_connection","category":"resource","severity":"info","status":"firing","scope":"instance",
			"affected_entities":[{"name":"db"}],"first_seen":"x","last_seen":"y","detection":{"family":"saturation"},"content":{"title":"t","summary":"s"},"requests":[]}]}`
		ctx, _ = serveDbo11y(t, `{"status":"success","data":`+asked+`}`)
		result, err = callDbo11yTool(ctx, ListDbo11yRecommendations, `{"include":["next_steps"]}`)
		require.NoError(t, err)
		text = dbo11yResultText(t, result)
		assert.Contains(t, text, `"next_steps":[]`)
		assert.NotContains(t, text, `"requests"`)
	})
}
