//go:build unit
// +build unit

package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestAgento11yManageAgents(t *testing.T) {
	const agentsPath = "/api/plugins/grafana-agento11y-app/resources/query/agents"

	testCases := []struct {
		name        string
		params      ManageAgento11yAgentsParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list sends the default page size and no filters",
			params: ManageAgento11yAgentsParams{Operation: "list"},
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
				require.True(t, ok, "list should return *agento11yListResponse[Agento11yAgent]")
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
			name: "list sends the name prefix and epoch second seen bounds",
			params: ManageAgento11yAgentsParams{
				Operation:  "list",
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
			name:   "list resolves relative time bounds to epoch seconds",
			params: ManageAgento11yAgentsParams{Operation: "list", StartTime: "now-7d", EndTime: "now"},
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
			name: "list forwards the cursor with the absolute filters unchanged",
			params: ManageAgento11yAgentsParams{
				Operation:  "list",
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
			name:    "list with a cursor and a relative start time is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list", Cursor: "agents-tok", StartTime: "now-7d"},
			wantErr: "start_time=\"now-7d\" is relative",
		},
		{
			name:    "list with a cursor and a relative end time is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list", Cursor: "agents-tok", EndTime: "now"},
			wantErr: "end_time=\"now\" is relative",
		},
		{
			// A bare duration has no "now" in it but still resolves against the
			// wall clock, so it drifts exactly like "now-7d" does.
			name:    "list with a cursor and a bare duration start time is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list", Cursor: "agents-tok", StartTime: "7d"},
			wantErr: "start_time=\"7d\" is relative",
		},
		{
			name:    "list with a cursor and a bare duration end time is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list", Cursor: "agents-tok", EndTime: "30m"},
			wantErr: "end_time=\"30m\" is relative",
		},
		{
			// An epoch in milliseconds is absolute, so it must not be mistaken
			// for a duration and rejected.
			name: "list with a cursor and epoch millisecond bounds is allowed",
			params: ManageAgento11yAgentsParams{
				Operation: "list",
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
			name:    "list with an unparseable start time is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list", StartTime: "not-a-date"},
			wantErr: "parsing start_time",
		},
		{
			name:    "list with an unparseable end time is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list", EndTime: "not-a-date"},
			wantErr: "parsing end_time",
		},
		{
			name:   "get sends the agent name and no version",
			params: ManageAgento11yAgentsParams{Operation: "get", AgentName: agento11yAgentName("claude-code")},
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
				require.True(t, ok, "get should return the raw agent object")
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
			name:   "get sends the name key for the unnamed agent",
			params: ManageAgento11yAgentsParams{Operation: "get", AgentName: agento11yAgentName("")},
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
			name: "get forwards an effective version",
			params: ManageAgento11yAgentsParams{
				Operation: "get",
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
			name: "get with a declared version is rejected",
			params: ManageAgento11yAgentsParams{
				Operation: "get",
				AgentName: agento11yAgentName("claude-code"),
				Version:   "1.4.2",
			},
			wantErr: "'sha256:<64 lowercase hex>'",
		},
		{
			name: "get with an uppercase hex version is rejected",
			params: ManageAgento11yAgentsParams{
				Operation: "get",
				AgentName: agento11yAgentName("claude-code"),
				Version:   "sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef",
			},
			wantErr: "must be an effective version",
		},
		{
			name: "get with a short hex version is rejected",
			params: ManageAgento11yAgentsParams{
				Operation: "get",
				AgentName: agento11yAgentName("claude-code"),
				Version:   "sha256:0123abcd",
			},
			wantErr: "must be an effective version",
		},
		{
			name:    "get without an agent name is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "get"},
			wantErr: "agent_name is required for \"get\" operation",
		},
		{
			name:   "get surfaces a 404 for an unknown agent",
			params: ManageAgento11yAgentsParams{Operation: "get", AgentName: agento11yAgentName("does-not-exist")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, err := w.Write([]byte("404 page not found\n"))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 404: 404 page not found",
		},
		{
			name:   "list_versions sends the name and the default page size",
			params: ManageAgento11yAgentsParams{Operation: "list_versions", AgentName: agento11yAgentName("claude-code")},
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
				require.True(t, ok, "list_versions should return *agento11yListResponse[Agento11yAgentVersion]")
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
			name:   "list_versions sends the name key for the unnamed agent",
			params: ManageAgento11yAgentsParams{Operation: "list_versions", AgentName: agento11yAgentName("")},
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
			name: "list_versions forwards the limit and cursor",
			params: ManageAgento11yAgentsParams{
				Operation: "list_versions",
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
			name:    "list_versions without an agent name is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list_versions"},
			wantErr: "agent_name is required for \"list_versions\" operation",
		},
		{
			name: "list_version_scores sends RFC3339 window bounds",
			params: ManageAgento11yAgentsParams{
				Operation: "list_version_scores",
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
				require.True(t, ok, "list_version_scores should return *agento11yListResponse[Agento11yAgentVersionScore]")
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
			name:   "list_version_scores without a window omits from, to, and cursor",
			params: ManageAgento11yAgentsParams{Operation: "list_version_scores", AgentName: agento11yAgentName("claude-code")},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				assert.Equal(t, "claude-code", query.Get("name"))
				for _, key := range []string{"from", "to", "cursor", "limit"} {
					assert.NotContains(t, query, key, "%s should not be sent by list_version_scores", key)
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
			name:    "list_version_scores without an agent name is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list_version_scores"},
			wantErr: "agent_name is required for 'list_version_scores' operation",
		},
		{
			name:    "list_version_scores with an empty agent name is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list_version_scores", AgentName: agento11yAgentName("")},
			wantErr: "agent_name must not be blank",
		},
		{
			name:    "list_version_scores with a whitespace-only agent name is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list_version_scores", AgentName: agento11yAgentName("   ")},
			wantErr: "agent_name must not be blank",
		},
		{
			name:    "version on list is rejected",
			params:  ManageAgento11yAgentsParams{Operation: "list", Version: agento11yEffectiveVersion},
			wantErr: "version is only valid for 'get' operation, not \"list\"",
		},
		{
			name: "version on list_versions is rejected",
			params: ManageAgento11yAgentsParams{
				Operation: "list_versions",
				AgentName: agento11yAgentName("claude-code"),
				Version:   agento11yEffectiveVersion,
			},
			wantErr: "version is only valid for 'get' operation",
		},
		{
			name: "version on list_version_scores is rejected",
			params: ManageAgento11yAgentsParams{
				Operation: "list_version_scores",
				AgentName: agento11yAgentName("claude-code"),
				Version:   agento11yEffectiveVersion,
			},
			wantErr: "version is only valid for 'get' operation",
		},
		{
			name:    "unknown operation names every supported operation",
			params:  ManageAgento11yAgentsParams{Operation: "delete"},
			wantErr: "unknown operation \"delete\", must be one of: list, get, list_versions, list_version_scores",
		},
		{
			name:    "empty operation is rejected",
			params:  ManageAgento11yAgentsParams{},
			wantErr: "unknown operation \"\"",
		},
		{
			name:   "upstream permission error is surfaced",
			params: ManageAgento11yAgentsParams{Operation: "list"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`{"error":"missing grafana-agento11y-app.data:read"}`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403: {\"error\":\"missing grafana-agento11y-app.data:read\"}",
		},
		{
			name:   "malformed list payload is a decode error",
			params: ManageAgento11yAgentsParams{Operation: "list"},
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

			result, err := manageAgento11yAgents(ctx, tc.params)
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

// TestAgento11yManageAgentsToolContract pins the parts of the advertised tool a
// client relies on but no request test touches: the read-only annotations that
// tell a caller it is safe to run, and the guidance that keeps it from fetching
// a huge prompt or filtering conversations by a key that does not exist.
func TestAgento11yManageAgentsToolContract(t *testing.T) {
	tool := ManageAgento11yAgents.Tool

	require.NotNil(t, tool.Annotations, "the tool should carry annotations")
	assert.True(t, tool.Annotations.ReadOnlyHint, "the tool should carry a read-only hint")
	assert.True(t, tool.Annotations.IdempotentHint, "the tool should carry an idempotent hint")

	for _, guidance := range []string{
		// Which changes mint a new version is the least obvious thing about the
		// catalog, and a tool edit never does.
		"never mints a new version",
		// A prompt edit only mints one when the agent declares no version of its
		// own, so the condition has to travel with the claim.
		"a hash of the system prompt",
		// 'get' can be tens of thousands of tokens, so the cheap size signal
		// has to be named.
		"token_estimate.total",
		// The catalog is only useful with the conversation tools.
		`agent = "<name>"`,
		// The cursor is bound to the filters it was issued with.
		"absolute RFC3339 times",
		// A 403 is unreadable without the role that causes it.
		"grafana-agento11y-app.data:read",
	} {
		assert.Contains(t, tool.Description, guidance)
	}

	// An agent version is not usable as a conversation-search filter key: the
	// Doris path has no column for it and generation search rejects it, so
	// advertising it here would send a model down a dead end.
	assert.NotContains(t, tool.Description, "agent.version")
	// No operation touches a route needing eval:write.
	assert.NotContains(t, tool.Description, "eval:write")
}

// TestAgento11yManageAgentsToolArgumentBinding calls through the registered
// tool handler rather than the Go function, because the whole point of making
// agent_name a *string is what happens to the JSON a client actually sends: an
// omitted key must stay omitted instead of arriving as "", which is how the
// unnamed agent is addressed.
func TestAgento11yManageAgentsToolArgumentBinding(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arguments map[string]any
		called    bool
		wantErr   string
	}{
		{
			name:      "an explicitly empty agent_name reaches the lookup route",
			arguments: map[string]any{"operation": "get", "agent_name": ""},
			called:    true,
		},
		{
			name:      "an omitted agent_name is rejected",
			arguments: map[string]any{"operation": "get"},
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

			result, err := ManageAgento11yAgents.Handler(ctx, newCallToolRequest("agento11y_manage_agents", tc.arguments))
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
		if text, ok := content.(*mcp.TextContent); ok {
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
