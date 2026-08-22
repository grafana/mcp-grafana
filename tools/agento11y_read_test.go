//go:build unit
// +build unit

package tools

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agento11yAgentName returns a pointer to name for the agent_name parameter.
func agento11yAgentName(name string) *string {
	return &name
}

// agento11yEffectiveVersion is a syntactically valid effective version: the
// "sha256:" prefix plus 64 lowercase hex characters.
const agento11yEffectiveVersion = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestAgento11yReadAgents(t *testing.T) {
	const agentsPath = "/api/plugins/grafana-agento11y-app/resources/query/agents"

	testCases := []struct {
		name        string
		params      Agento11yReadParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list_agents sends the default page size and no filters",
			params: Agento11yReadParams{Operation: "list_agents"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, agentsPath, r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

				query := r.URL.Query()
				assert.Equal(t, "50", query.Get("limit"))
				for _, key := range []string{"cursor", "name_prefix", "seen_after", "seen_before"} {
					assert.NotContains(t, query, key, "%s should not be sent when it was not requested", key)
				}

				writeJSONResponse(t, w, `{"items":[{
					"agent_name": "claude-code",
					"latest_effective_version": "`+agento11yEffectiveVersion+`",
					"latest_declared_version": "1.4.2",
					"first_seen_at": "2026-07-01T10:00:00Z",
					"latest_seen_at": "2026-07-30T09:00:00Z",
					"generation_count": 4210,
					"version_count": 3,
					"tool_count": 12,
					"system_prompt_prefix": "You are Claude Code",
					"token_estimate": {"system_prompt": 1800, "tools_total": 9400, "total": 11200}
				},{
					"agent_name": "",
					"latest_effective_version": "`+agento11yEffectiveVersion+`",
					"generation_count": 7,
					"version_count": 1,
					"tool_count": 0,
					"system_prompt_prefix": "",
					"token_estimate": {"system_prompt": 0, "tools_total": 0, "total": 0}
				}],"next_cursor":"agents-tok"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yAgent])
				require.True(t, ok, "list_agents should return *agento11yListResponse[Agento11yAgent]")
				require.Len(t, resp.Items, 2)

				named := resp.Items[0]
				assert.Equal(t, "claude-code", named.AgentName)
				assert.Equal(t, agento11yEffectiveVersion, named.LatestEffectiveVersion)
				require.NotNil(t, named.LatestDeclaredVersion, "a declared version present on the wire should decode")
				assert.Equal(t, "1.4.2", *named.LatestDeclaredVersion)
				assert.True(t, named.FirstSeenAt.Equal(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)))
				assert.True(t, named.LatestSeenAt.Equal(time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)))
				assert.Equal(t, int64(4210), named.GenerationCount)
				assert.Equal(t, 3, named.VersionCount)
				assert.Equal(t, 12, named.ToolCount)
				assert.Equal(t, "You are Claude Code", named.SystemPromptPrefix)
				assert.Equal(t, 1800, named.TokenEstimate.SystemPrompt)
				assert.Equal(t, 9400, named.TokenEstimate.ToolsTotal)
				assert.Equal(t, 11200, named.TokenEstimate.Total)

				// The API omits latest_declared_version for an agent that never
				// declared one.
				assert.Nil(t, resp.Items[1].LatestDeclaredVersion)
				assert.Empty(t, resp.Items[1].AgentName)

				assert.Equal(t, "agents-tok", resp.NextCursor)
			},
		},
		{
			name: "list_agents sends the name prefix and epoch second seen bounds",
			params: Agento11yReadParams{
				Operation:  "list_agents",
				NamePrefix: "claude",
				StartTime:  "2026-07-01T10:00:00Z",
				EndTime:    "2026-07-30T09:00:00Z",
				Limit:      25,
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				assert.Equal(t, "claude", query.Get("name_prefix"))
				assert.Equal(t, "25", query.Get("limit"))
				// This route takes unix epoch seconds, not RFC3339.
				assert.Equal(t, strconv.FormatInt(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Unix(), 10), query.Get("seen_after"))
				assert.Equal(t, strconv.FormatInt(time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC).Unix(), 10), query.Get("seen_before"))

				writeJSONResponse(t, w, `{"items":[]}`)
			},
		},
		{
			name:   "list_agents resolves relative time bounds to epoch seconds",
			params: Agento11yReadParams{Operation: "list_agents", StartTime: "now-7d", EndTime: "now"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()

				seenAfter, err := strconv.ParseInt(query.Get("seen_after"), 10, 64)
				require.NoError(t, err, "seen_after should be an integer number of seconds")
				assert.WithinDuration(t, time.Now().Add(-7*24*time.Hour), time.Unix(seenAfter, 0), time.Minute)

				seenBefore, err := strconv.ParseInt(query.Get("seen_before"), 10, 64)
				require.NoError(t, err, "seen_before should be an integer number of seconds")
				assert.WithinDuration(t, time.Now(), time.Unix(seenBefore, 0), time.Minute)

				writeJSONResponse(t, w, `{"items":[]}`)
			},
		},
		{
			name: "list_agents forwards the cursor with the absolute filters unchanged",
			params: Agento11yReadParams{
				Operation:  "list_agents",
				NamePrefix: "claude",
				StartTime:  "2026-07-01T10:00:00Z",
				EndTime:    "2026-07-30T09:00:00Z",
				Cursor:     "agents-tok",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				assert.Equal(t, "agents-tok", query.Get("cursor"))
				assert.Equal(t, "claude", query.Get("name_prefix"))
				assert.Equal(t, strconv.FormatInt(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Unix(), 10), query.Get("seen_after"))
				assert.Equal(t, strconv.FormatInt(time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC).Unix(), 10), query.Get("seen_before"))

				writeJSONResponse(t, w, `{"items":[{"agent_name":"claude-code"}],"next_cursor":"agents-tok-2"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yAgent])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "agents-tok-2", resp.NextCursor)
			},
		},
		{
			name:    "list_agents with a cursor and a relative start time is rejected",
			params:  Agento11yReadParams{Operation: "list_agents", Cursor: "agents-tok", StartTime: "now-7d"},
			wantErr: "start_time=\"now-7d\" is relative",
		},
		{
			name:    "list_agents with a cursor and a relative end time is rejected",
			params:  Agento11yReadParams{Operation: "list_agents", Cursor: "agents-tok", EndTime: "now"},
			wantErr: "end_time=\"now\" is relative",
		},
		{
			// A bare duration has no "now" in it but still resolves against the
			// wall clock, so it drifts exactly like "now-7d" does.
			name:    "list_agents with a cursor and a bare duration start time is rejected",
			params:  Agento11yReadParams{Operation: "list_agents", Cursor: "agents-tok", StartTime: "7d"},
			wantErr: "start_time=\"7d\" is relative",
		},
		{
			name:    "list_agents with a cursor and a bare duration end time is rejected",
			params:  Agento11yReadParams{Operation: "list_agents", Cursor: "agents-tok", EndTime: "30m"},
			wantErr: "end_time=\"30m\" is relative",
		},
		{
			// An epoch in milliseconds is absolute, so it must not be mistaken
			// for a duration and rejected.
			name: "list_agents with a cursor and epoch millisecond bounds is allowed",
			params: Agento11yReadParams{
				Operation: "list_agents",
				Cursor:    "agents-tok",
				StartTime: "1767225600000",
				EndTime:   "1767830400000",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				assert.Equal(t, "agents-tok", query.Get("cursor"))
				assert.Equal(t, "1767225600", query.Get("seen_after"))
				assert.Equal(t, "1767830400", query.Get("seen_before"))

				writeJSONResponse(t, w, `{"items":[]}`)
			},
		},
		{
			name:    "list_agents with an unparseable start time is rejected",
			params:  Agento11yReadParams{Operation: "list_agents", StartTime: "not-a-date"},
			wantErr: "parsing start_time",
		},
		{
			name:    "list_agents with an unparseable end time is rejected",
			params:  Agento11yReadParams{Operation: "list_agents", EndTime: "not-a-date"},
			wantErr: "parsing end_time",
		},
		{
			name:   "get_agent sends the agent name and no version",
			params: Agento11yReadParams{Operation: "get_agent", AgentName: agento11yAgentName("claude-code")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, agentsPath+"/lookup", r.URL.Path)

				query := r.URL.Query()
				assert.Equal(t, "claude-code", query.Get("name"))
				assert.NotContains(t, query, "version", "the latest version is requested by omitting version")

				writeJSONResponse(t, w, `{
					"agent_name": "claude-code",
					"effective_version": "`+agento11yEffectiveVersion+`",
					"declared_version_latest": "1.4.2",
					"system_prompt": "You are Claude Code, a coding agent.",
					"tool_count": 1,
					"token_estimate": {"system_prompt": 1800, "tools_total": 9400, "total": 11200},
					"tools": [{"name": "read", "type": "function", "input_schema_json": "{\"type\":\"object\"}", "token_estimate": 120}],
					"models": [{"provider": "anthropic", "name": "claude-opus-4-6", "generation_count": 4210}]
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok, "get_agent should return the raw agent object")
				assert.Equal(t, "claude-code", detail["agent_name"])
				assert.Equal(t, agento11yEffectiveVersion, detail["effective_version"])
				assert.Equal(t, "You are Claude Code, a coding agent.", detail["system_prompt"])
				assert.Equal(t, "1.4.2", detail["declared_version_latest"])
				// Nothing is trimmed out of the payload: the tool schemas and the
				// model list reach the caller as sent.
				assert.Len(t, detail["tools"], 1)
				assert.Len(t, detail["models"], 1)
				assert.Contains(t, detail, "token_estimate")
			},
		},
		{
			name:   "get_agent sends the name key for the unnamed agent",
			params: Agento11yReadParams{Operation: "get_agent", AgentName: agento11yAgentName("")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				// The upstream handler answers 400 when the key is absent and
				// treats an empty value as the unnamed agent, so the key must be
				// present even when the value is empty.
				assert.Contains(t, r.URL.Query(), "name", "the name key must be sent even when empty")
				assert.Equal(t, "", r.URL.Query().Get("name"))

				writeJSONResponse(t, w, `{"agent_name":"","effective_version":"`+agento11yEffectiveVersion+`"}`)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "", detail["agent_name"])
			},
		},
		{
			name: "get_agent forwards an effective version",
			params: Agento11yReadParams{
				Operation: "get_agent",
				AgentName: agento11yAgentName("claude-code"),
				Version:   agento11yEffectiveVersion,
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "claude-code", r.URL.Query().Get("name"))
				assert.Equal(t, agento11yEffectiveVersion, r.URL.Query().Get("version"))

				writeJSONResponse(t, w, `{"agent_name":"claude-code","effective_version":"`+agento11yEffectiveVersion+`"}`)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, agento11yEffectiveVersion, detail["effective_version"])
			},
		},
		{
			name: "get_agent with a declared version is rejected",
			params: Agento11yReadParams{
				Operation: "get_agent",
				AgentName: agento11yAgentName("claude-code"),
				Version:   "1.4.2",
			},
			wantErr: "'sha256:<64 lowercase hex>'",
		},
		{
			name: "get_agent with an uppercase hex version is rejected",
			params: Agento11yReadParams{
				Operation: "get_agent",
				AgentName: agento11yAgentName("claude-code"),
				Version:   "sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef",
			},
			wantErr: "must be an effective version",
		},
		{
			name: "get_agent with a short hex version is rejected",
			params: Agento11yReadParams{
				Operation: "get_agent",
				AgentName: agento11yAgentName("claude-code"),
				Version:   "sha256:0123abcd",
			},
			wantErr: "must be an effective version",
		},
		{
			name:    "get_agent without an agent name is rejected",
			params:  Agento11yReadParams{Operation: "get_agent"},
			wantErr: "agent_name is required for \"get_agent\" operation",
		},
		{
			name:   "get_agent surfaces a 404 for an unknown agent",
			params: Agento11yReadParams{Operation: "get_agent", AgentName: agento11yAgentName("does-not-exist")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, err := w.Write([]byte("404 page not found\n"))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 404: 404 page not found",
		},
		{
			name:   "list_agent_versions sends the name and the default page size",
			params: Agento11yReadParams{Operation: "list_agent_versions", AgentName: agento11yAgentName("claude-code")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, agentsPath+"/versions", r.URL.Path)

				query := r.URL.Query()
				assert.Equal(t, "claude-code", query.Get("name"))
				assert.Equal(t, "50", query.Get("limit"))
				assert.NotContains(t, query, "cursor")

				writeJSONResponse(t, w, `{"items":[{
					"effective_version": "`+agento11yEffectiveVersion+`",
					"declared_version_first": "1.4.0",
					"declared_version_latest": "1.4.2",
					"first_seen_at": "2026-07-01T10:00:00Z",
					"last_seen_at": "2026-07-30T09:00:00Z",
					"generation_count": 4210,
					"tool_count": 12,
					"system_prompt_prefix": "You are Claude Code",
					"token_estimate": {"system_prompt": 1800, "tools_total": 9400, "total": 11200}
				},{
					"effective_version": "`+agento11yEffectiveVersion+`",
					"generation_count": 3,
					"tool_count": 0,
					"token_estimate": {"system_prompt": 0, "tools_total": 0, "total": 0}
				}]}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yAgentVersion])
				require.True(t, ok, "list_agent_versions should return *agento11yListResponse[Agento11yAgentVersion]")
				require.Len(t, resp.Items, 2)

				version := resp.Items[0]
				assert.Equal(t, agento11yEffectiveVersion, version.EffectiveVersion)
				require.NotNil(t, version.DeclaredVersionFirst)
				assert.Equal(t, "1.4.0", *version.DeclaredVersionFirst)
				require.NotNil(t, version.DeclaredVersionLatest)
				assert.Equal(t, "1.4.2", *version.DeclaredVersionLatest)
				assert.True(t, version.FirstSeenAt.Equal(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)))
				assert.True(t, version.LastSeenAt.Equal(time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)))
				assert.Equal(t, int64(4210), version.GenerationCount)
				assert.Equal(t, 12, version.ToolCount)
				assert.Equal(t, "You are Claude Code", version.SystemPromptPrefix)
				assert.Equal(t, 11200, version.TokenEstimate.Total)

				assert.Nil(t, resp.Items[1].DeclaredVersionFirst)
				assert.Nil(t, resp.Items[1].DeclaredVersionLatest)
				assert.Empty(t, resp.NextCursor)
			},
		},
		{
			name:   "list_agent_versions sends the name key for the unnamed agent",
			params: Agento11yReadParams{Operation: "list_agent_versions", AgentName: agento11yAgentName("")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				assert.Contains(t, r.URL.Query(), "name", "the name key must be sent even when empty")
				assert.Equal(t, "", r.URL.Query().Get("name"))

				writeJSONResponse(t, w, `{"items":[{"effective_version":"`+agento11yEffectiveVersion+`"}],"next_cursor":"versions-tok"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yAgentVersion])
				require.True(t, ok)
				assert.Equal(t, "versions-tok", resp.NextCursor)
			},
		},
		{
			name: "list_agent_versions forwards the limit and cursor",
			params: Agento11yReadParams{
				Operation: "list_agent_versions",
				AgentName: agento11yAgentName("claude-code"),
				Limit:     10,
				Cursor:    "versions-tok",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				assert.Equal(t, "claude-code", query.Get("name"))
				assert.Equal(t, "10", query.Get("limit"))
				assert.Equal(t, "versions-tok", query.Get("cursor"))

				writeJSONResponse(t, w, `{"items":[],"next_cursor":"versions-tok-2"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yAgentVersion])
				require.True(t, ok)
				assert.Equal(t, "versions-tok-2", resp.NextCursor)
			},
		},
		{
			name:    "list_agent_versions without an agent name is rejected",
			params:  Agento11yReadParams{Operation: "list_agent_versions"},
			wantErr: "agent_name is required for \"list_agent_versions\" operation",
		},
		{
			name: "list_agent_version_scores sends RFC3339 window bounds",
			params: Agento11yReadParams{
				Operation: "list_agent_version_scores",
				AgentName: agento11yAgentName("claude-code"),
				StartTime: "2026-07-01T10:00:00Z",
				EndTime:   "2026-07-30T09:00:00Z",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, agentsPath+"/version-scores", r.URL.Path)

				query := r.URL.Query()
				assert.Equal(t, "claude-code", query.Get("name"))
				// This route takes RFC3339, unlike the epoch seconds of /query/agents.
				assert.Equal(t, "2026-07-01T10:00:00Z", query.Get("from"))
				assert.Equal(t, "2026-07-30T09:00:00Z", query.Get("to"))

				writeJSONResponse(t, w, `{"items":[{
					"effective_version": "`+agento11yEffectiveVersion+`",
					"declared_version": "1.4.2",
					"evaluators": [
						{"score_key": "helpfulness", "total_scores": 10, "pass_count": 8, "fail_count": 2, "mean_score": 0.82},
						{"score_key": "refusal", "total_scores": 4, "pass_count": 4, "fail_count": 0}
					],
					"total_scores": 14,
					"pass_count": 12,
					"fail_count": 2,
					"first_seen_at": "2026-07-02T10:00:00Z",
					"last_seen_at": "2026-07-29T09:00:00Z"
				}]}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yAgentVersionScore])
				require.True(t, ok, "list_agent_version_scores should return *agento11yListResponse[Agento11yAgentVersionScore]")
				require.Len(t, resp.Items, 1)

				item := resp.Items[0]
				assert.Equal(t, agento11yEffectiveVersion, item.EffectiveVersion)
				require.NotNil(t, item.DeclaredVersion)
				assert.Equal(t, "1.4.2", *item.DeclaredVersion)
				assert.Equal(t, 14, item.TotalScores)
				assert.Equal(t, 12, item.PassCount)
				assert.Equal(t, 2, item.FailCount)
				assert.True(t, item.FirstSeenAt.Equal(time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)))
				assert.True(t, item.LastSeenAt.Equal(time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)))

				require.Len(t, item.Evaluators, 2)
				assert.Equal(t, "helpfulness", item.Evaluators[0].ScoreKey)
				assert.Equal(t, 10, item.Evaluators[0].TotalScores)
				assert.Equal(t, 8, item.Evaluators[0].PassCount)
				assert.Equal(t, 2, item.Evaluators[0].FailCount)
				require.NotNil(t, item.Evaluators[0].MeanScore)
				assert.Equal(t, 0.82, *item.Evaluators[0].MeanScore)
				// A non-numeric score type has no mean, which must stay
				// distinguishable from a mean of zero.
				assert.Nil(t, item.Evaluators[1].MeanScore)

				// The route has no pagination.
				assert.Empty(t, resp.NextCursor)
			},
		},
		{
			name:   "list_agent_version_scores without a window omits from, to, and cursor",
			params: Agento11yReadParams{Operation: "list_agent_version_scores", AgentName: agento11yAgentName("claude-code")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				assert.Equal(t, "claude-code", query.Get("name"))
				for _, key := range []string{"from", "to", "cursor", "limit"} {
					assert.NotContains(t, query, key, "%s should not be sent by list_agent_version_scores", key)
				}

				writeJSONResponse(t, w, `{"items":[]}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yAgentVersionScore])
				require.True(t, ok)
				assert.Empty(t, resp.Items)
				assert.Empty(t, resp.NextCursor)
			},
		},
		{
			name:    "list_agent_version_scores without an agent name is rejected",
			params:  Agento11yReadParams{Operation: "list_agent_version_scores"},
			wantErr: "agent_name is required for 'list_agent_version_scores' operation",
		},
		{
			name:    "list_agent_version_scores with an empty agent name is rejected",
			params:  Agento11yReadParams{Operation: "list_agent_version_scores", AgentName: agento11yAgentName("")},
			wantErr: "agent_name must not be blank",
		},
		{
			name:    "list_agent_version_scores with a whitespace-only agent name is rejected",
			params:  Agento11yReadParams{Operation: "list_agent_version_scores", AgentName: agento11yAgentName("   ")},
			wantErr: "agent_name must not be blank",
		},
		{
			name:    "version on list_agents is rejected",
			params:  Agento11yReadParams{Operation: "list_agents", Version: agento11yEffectiveVersion},
			wantErr: "version is only valid for 'get_agent' operation, not \"list_agents\"",
		},
		{
			name: "version on list_agent_versions is rejected",
			params: Agento11yReadParams{
				Operation: "list_agent_versions",
				AgentName: agento11yAgentName("claude-code"),
				Version:   agento11yEffectiveVersion,
			},
			wantErr: "version is only valid for 'get_agent' operation",
		},
		{
			name: "version on list_agent_version_scores is rejected",
			params: Agento11yReadParams{
				Operation: "list_agent_version_scores",
				AgentName: agento11yAgentName("claude-code"),
				Version:   agento11yEffectiveVersion,
			},
			wantErr: "version is only valid for 'get_agent' operation",
		},
		{
			name:    "unknown operation names every supported operation",
			params:  Agento11yReadParams{Operation: "delete"},
			wantErr: "unknown operation \"delete\", must be one of: " + agento11yReadOperations,
		},
		{
			name:    "empty operation is rejected",
			params:  Agento11yReadParams{},
			wantErr: "unknown operation \"\"",
		},
		{
			name:   "upstream permission error is surfaced",
			params: Agento11yReadParams{Operation: "list_agents"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`{"error":"missing grafana-agento11y-app.data:read"}`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403: {\"error\":\"missing grafana-agento11y-app.data:read\"}",
		},
		{
			name:   "malformed list_agents payload is a decode error",
			params: Agento11yReadParams{Operation: "list_agents"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				writeJSONResponse(t, w, `{not json`)
			},
			wantErr: "failed to decode GET /query/agents response",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler == nil {
					t.Error("server should not be called for validation failures")
					return
				}
				tc.handler(t, w, r)
			})
			defer server.Close()

			result, err := agento11yRead(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}

// TestAgento11yReadToolContract pins the parts of the advertised tool a
// client relies on but no request test touches: the read-only annotations that
// tell a caller it is safe to run, and the guidance that keeps it from fetching
// a huge prompt or filtering conversations by a key that does not exist.
func TestAgento11yReadToolContract(t *testing.T) {
	tool := Agento11yRead.Tool

	require.NotNil(t, tool.Annotations.ReadOnlyHint, "the tool should carry a read-only hint")
	assert.True(t, *tool.Annotations.ReadOnlyHint)
	require.NotNil(t, tool.Annotations.IdempotentHint, "the tool should carry an idempotent hint")
	assert.True(t, *tool.Annotations.IdempotentHint)

	for _, guidance := range []string{
		// Which changes mint a new version is the least obvious thing about the
		// catalog, and a tool edit never does.
		"never mints a new version",
		// A prompt edit only mints one when the agent declares no version of its
		// own, so the condition has to travel with the claim.
		"a hash of the system prompt",
		// 'get_agent' can be tens of thousands of tokens, so the cheap size
		// signal has to be named.
		"token_estimate.total",
		// The catalog is only useful with the conversation operations, now
		// merged into the same tool.
		`agent = "<name>"`,
		// The cursor is bound to the filters it was issued with.
		"absolute RFC3339 times",
		// A 403 is unreadable without the role that causes it.
		"grafana-agento11y-app.data:read",
	} {
		assert.Contains(t, tool.Description, guidance)
	}

	// No operation touches a route needing eval:write: agento11y_read has no
	// write half.
	assert.NotContains(t, tool.Description, "eval:write")
}

// TestAgento11yReadToolArgumentBinding calls through the registered tool
// handler rather than the Go function, because the whole point of making
// agent_name a *string is what happens to the JSON a client actually sends: an
// omitted key must stay omitted instead of arriving as "", which is how the
// unnamed agent is addressed.
func TestAgento11yReadToolArgumentBinding(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arguments map[string]any
		called    bool
		wantErr   string
	}{
		{
			name:      "an explicitly empty agent_name reaches the lookup route",
			arguments: map[string]any{"operation": "get_agent", "agent_name": ""},
			called:    true,
		},
		{
			name:      "an omitted agent_name is rejected",
			arguments: map[string]any{"operation": "get_agent"},
			wantErr:   "agent_name is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				called = true
				assert.Contains(t, r.URL.Query(), "name", "the name key must be sent even when empty")
				assert.Equal(t, "", r.URL.Query().Get("name"))
				writeJSONResponse(t, w, `{"agent_name":""}`)
			})
			defer server.Close()

			result, err := Agento11yRead.Handler(ctx, mcp.CallToolRequest{
				Params: mcp.CallToolParams{Name: "agento11y_read", Arguments: tc.arguments},
			})
			require.NoError(t, err, "the MCP handler reports tool failures in the result, not as a transport error")
			require.NotNil(t, result)

			assert.Equal(t, tc.called, called, "whether the plugin was contacted")
			if tc.wantErr != "" {
				require.True(t, result.IsError, "expected a tool error result")
				assert.Contains(t, resultText(t, result), tc.wantErr)
				return
			}
			assert.False(t, result.IsError, "unexpected tool error: %s", resultText(t, result))
		})
	}
}

// resultText joins the text content of a tool result.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// TestAgento11yAgentRelativeTimeBound covers the classification that decides
// whether a cursor call is rejected. The time parser reads a bare duration such
// as "7d" as "7 days before now", so it drifts between pages just like
// "now-7d", while an integer is an absolute epoch in milliseconds.
func TestAgento11yAgentRelativeTimeBound(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "now", want: true},
		{value: "now-7d", want: true},
		{value: "now/d", want: true},
		{value: "  now-1h  ", want: true},
		{value: "7d", want: true},
		{value: "1h", want: true},
		{value: "30m", want: true},
		{value: "2w", want: true},
		{value: "1767225600000", want: false},
		{value: "2026-07-01T10:00:00Z", want: false},
		{value: "2026-07-01T10:00:00-05:00", want: false},
		{value: "2026-07-01", want: false},
	} {
		assert.Equal(t, tc.want, agento11yRelativeTimeBound(tc.value), "relative classification of %q", tc.value)
	}
}

// TestAgento11yAgentEffectiveVersionPattern pins the version shape the tool
// accepts. That check is the only thing standing between a declared version and
// a bare 400 from the API.
func TestAgento11yAgentEffectiveVersionPattern(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{version: agento11yEffectiveVersion, want: true},
		{version: "sha256:" + strings.Repeat("a", 64), want: true},
		{version: "sha256:" + strings.Repeat("a", 63), want: false},
		{version: "sha256:" + strings.Repeat("a", 65), want: false},
		{version: "sha1:" + strings.Repeat("a", 64), want: false},
		{version: strings.Repeat("a", 64), want: false},
		{version: "1.4.2", want: false},
		{version: "", want: false},
	} {
		assert.Equal(t, tc.want, agento11yEffectiveVersionPattern.MatchString(tc.version), "pattern match for %q", tc.version)
	}
}

func TestAgento11yReadConversations(t *testing.T) {
	testCases := []struct {
		name        string
		params      Agento11yReadParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list_conversations recent conversations",
			params: Agento11yReadParams{Operation: "list_conversations"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/query/conversations", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Equal(t, "50", r.URL.Query().Get("limit"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"items":[{
					"id": "conv-1",
					"title": "Hello",
					"generation_count": 2,
					"annotation_summary": {"annotation_count": 3, "latest_annotation_type": "note", "latest_annotated_at": "2025-04-23T10:00:00Z"}
				}],"next_cursor":"list-tok"}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yConversation])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "conv-1", resp.Items[0].ID)
				assert.Equal(t, "Hello", resp.Items[0].Title)
				assert.Equal(t, 2, resp.Items[0].GenerationCount)
				require.NotNil(t, resp.Items[0].AnnotationSummary)
				assert.Equal(t, 3, resp.Items[0].AnnotationSummary.AnnotationCount)
				assert.Equal(t, "note", resp.Items[0].AnnotationSummary.LatestAnnotationType)
				assert.Equal(t, "list-tok", resp.NextCursor)
			},
		},
		{
			name:   "list_conversations passes limit and cursor through",
			params: Agento11yReadParams{Operation: "list_conversations", Limit: 10, Cursor: "abc"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "10", r.URL.Query().Get("limit"))
				require.Equal(t, "abc", r.URL.Query().Get("cursor"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"items":[]}`))
				require.NoError(t, err)
			},
		},
		{
			name: "search_conversations with filters and concrete time range",
			params: Agento11yReadParams{
				Operation: "search_conversations",
				Filters:   `status = "error"`,
				StartTime: "2025-04-23T10:00:00Z",
				EndTime:   "2025-04-23T11:00:00Z",
				Limit:     25,
				Cursor:    "cursor-1",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/query/conversations/search", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))

				var req Agento11ySearchRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				assert.Equal(t, `status = "error"`, req.Filters)
				assert.Equal(t, 25, req.PageSize)
				assert.Equal(t, "cursor-1", req.Cursor)
				require.NotNil(t, req.TimeRange)
				assert.True(t, req.TimeRange.From.Equal(time.Date(2025, 4, 23, 10, 0, 0, 0, time.UTC)))
				assert.True(t, req.TimeRange.To.Equal(time.Date(2025, 4, 23, 11, 0, 0, 0, time.UTC)))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{
					"conversations": [{
						"conversation_id": "conv-1",
						"conversation_title": "Broken run",
						"generation_count": 3,
						"models": ["claude-opus-4-6"],
						"agents": ["claude-code"],
						"error_count": 2,
						"has_errors": true,
						"trace_ids": ["trace-1"],
						"rating_summary": {"total_count": 1, "good_count": 0, "bad_count": 1, "latest_rated_at": "2025-04-23T10:30:00Z", "latest_bad_at": "2025-04-23T10:30:00Z", "has_bad_rating": true},
						"annotation_count": 0,
						"eval_summary": {"total_scores": 4, "pass_count": 3, "fail_count": 1}
					}],
					"next_cursor": "next-tok",
					"has_more": true
				}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11ySearchResponse)
				require.True(t, ok)
				require.Len(t, resp.Conversations, 1)
				conv := resp.Conversations[0]
				assert.Equal(t, "conv-1", conv.ConversationID)
				assert.Equal(t, 2, conv.ErrorCount)
				assert.True(t, conv.HasErrors)
				require.NotNil(t, conv.RatingSummary)
				assert.True(t, conv.RatingSummary.HasBadRating)
				assert.True(t, conv.RatingSummary.LatestRatedAt.Equal(time.Date(2025, 4, 23, 10, 30, 0, 0, time.UTC)))
				assert.True(t, conv.RatingSummary.LatestBadAt.Equal(time.Date(2025, 4, 23, 10, 30, 0, 0, time.UTC)))
				require.NotNil(t, conv.EvalSummary)
				assert.Equal(t, 1, conv.EvalSummary.FailCount)
				assert.Equal(t, "next-tok", resp.NextCursor)
				assert.True(t, resp.HasMore)
			},
		},
		{
			name:   "search_conversations defaults to last 24 hours",
			params: Agento11yReadParams{Operation: "search_conversations"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				var req Agento11ySearchRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				require.NotNil(t, req.TimeRange)
				assert.WithinDuration(t, time.Now(), req.TimeRange.To, time.Minute)
				assert.WithinDuration(t, time.Now().Add(-24*time.Hour), req.TimeRange.From, time.Minute)
				assert.Equal(t, 50, req.PageSize)

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"conversations":[],"has_more":false}`))
				require.NoError(t, err)
			},
		},
		{
			name:   "get_conversation detail",
			params: Agento11yReadParams{Operation: "get_conversation", ConversationID: "conv-123"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/query/conversations/conv-123", r.URL.Path)

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"id":"conv-123","generations":[{"id":"gen-1"}]}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "conv-123", detail["id"])
				assert.Len(t, detail["generations"], 1)
			},
		},
		{
			name:    "get_conversation without conversation_id",
			params:  Agento11yReadParams{Operation: "get_conversation"},
			wantErr: "conversation_id is required",
		},
		{
			name:    "unknown operation",
			params:  Agento11yReadParams{Operation: "delete_conversation"},
			wantErr: "unknown operation",
		},
		{
			name:    "search_conversations with invalid start time",
			params:  Agento11yReadParams{Operation: "search_conversations", StartTime: "not-a-date"},
			wantErr: "parsing start_time",
		},
		{
			name:    "search_conversations with cursor but no explicit time range is rejected",
			params:  Agento11yReadParams{Operation: "search_conversations", Cursor: "tok-1"},
			wantErr: "paginating with a cursor requires",
		},
		{
			name: "search_conversations with cursor and explicit time range passes bounds through unchanged",
			params: Agento11yReadParams{
				Operation: "search_conversations",
				Filters:   `status = "error"`,
				StartTime: "2025-04-23T10:00:00Z",
				EndTime:   "2025-04-23T11:00:00Z",
				Cursor:    "tok-1",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				var req Agento11ySearchRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				assert.Equal(t, "tok-1", req.Cursor)
				assert.Equal(t, `status = "error"`, req.Filters)
				require.NotNil(t, req.TimeRange)
				assert.True(t, req.TimeRange.From.Equal(time.Date(2025, 4, 23, 10, 0, 0, 0, time.UTC)))
				assert.True(t, req.TimeRange.To.Equal(time.Date(2025, 4, 23, 11, 0, 0, 0, time.UTC)))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"conversations":[],"has_more":false}`))
				require.NoError(t, err)
			},
		},
		{
			name:   "upstream error is returned",
			params: Agento11yReadParams{Operation: "list_conversations"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`{"error":"missing grafana-agento11y-app.conversations:read"}`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403",
		},
		{
			name:   "oversized response is rejected",
			params: Agento11yReadParams{Operation: "get_conversation", ConversationID: "conv-big"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				_, err := w.Write(make([]byte, defaultResponseLimitBytes+1))
				require.NoError(t, err)
			},
			wantErr: "exceeds maximum size",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler == nil {
					t.Error("server should not be called for validation failures")
					return
				}
				tc.handler(t, w, r)
			})
			defer server.Close()

			result, err := agento11yRead(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}

func TestAgento11yReadGenerations(t *testing.T) {
	testCases := []struct {
		name        string
		params      Agento11yReadParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "get_generation detail",
			params: Agento11yReadParams{Operation: "get_generation", GenerationID: "gen-123"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/query/generations/gen-123", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"id":"gen-123","model":{"name":"claude-opus-4-6"},"status":"error"}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "gen-123", detail["id"])
				assert.Equal(t, "error", detail["status"])
			},
		},
		{
			name:   "list_generation_scores",
			params: Agento11yReadParams{Operation: "list_generation_scores", GenerationID: "gen-123"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/query/generations/gen-123/scores", r.URL.Path)
				require.Equal(t, "50", r.URL.Query().Get("limit"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{
					"items": [{
						"score_id": "score-1",
						"generation_id": "gen-123",
						"evaluator_id": "eval-1",
						"evaluator_version": "v1",
						"experiment_id": "exp-1",
						"score_key": "helpfulness",
						"score_type": "number",
						"value": {"number": 0.9},
						"passed": true,
						"explanation": "response addressed the question"
					}],
					"next_cursor": "score-tok"
				}`))
				require.NoError(t, err)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yScore])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				score := resp.Items[0]
				assert.Equal(t, "eval-1", score.EvaluatorID)
				assert.Equal(t, "exp-1", score.ExperimentID)
				assert.Equal(t, "helpfulness", score.ScoreKey)
				assert.Equal(t, "number", score.ScoreType)
				require.NotNil(t, score.Value.Number)
				assert.Equal(t, 0.9, *score.Value.Number)
				require.NotNil(t, score.Passed)
				assert.True(t, *score.Passed)
				assert.Equal(t, "response addressed the question", score.Explanation)
				assert.Equal(t, "score-tok", resp.NextCursor)
			},
		},
		{
			name:   "list_generation_scores passes limit and cursor through",
			params: Agento11yReadParams{Operation: "list_generation_scores", GenerationID: "gen-123", Limit: 5, Cursor: "tok-2"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "5", r.URL.Query().Get("limit"))
				require.Equal(t, "tok-2", r.URL.Query().Get("cursor"))

				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"items":[]}`))
				require.NoError(t, err)
			},
		},
		{
			name:    "get_generation without generation_id",
			params:  Agento11yReadParams{Operation: "get_generation"},
			wantErr: "generation_id is required",
		},
		{
			name:    "list_generation_scores without generation_id",
			params:  Agento11yReadParams{Operation: "list_generation_scores"},
			wantErr: "generation_id is required",
		},
		{
			name:    "unknown operation",
			params:  Agento11yReadParams{Operation: "list", GenerationID: "gen-123"},
			wantErr: "unknown operation",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler == nil {
					t.Error("server should not be called for validation failures")
					return
				}
				tc.handler(t, w, r)
			})
			defer server.Close()

			result, err := agento11yRead(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}
