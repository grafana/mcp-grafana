//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agento11yPtr returns a pointer to v, for the *string/*[]string fields
// Agento11yEvalsWriteParams shares across sub-domains (nil means "omitted",
// distinct from an explicitly zero value).
func agento11yPtr[T any](v T) *T {
	return &v
}

func TestAgento11yEvalsReadEvaluators(t *testing.T) {
	testCases := []struct {
		name        string
		params      Agento11yEvalsReadParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list evaluators sends the default limit",
			params: Agento11yEvalsReadParams{Operation: "list_evaluators"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/evaluators", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Equal(t, "50", r.URL.Query().Get("limit"))
				require.False(t, r.URL.Query().Has("cursor"))

				writeJSONResponse(t, w, `{"items":[{
					"evaluator_id": "quality.helpfulness",
					"version": "v1",
					"kind": "llm_judge",
					"config": {"model": "gpt-4o"},
					"output_keys": [{"key": "helpfulness", "type": "number", "pass_threshold": 0.7}]
				}],"next_cursor":"42"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yEvaluatorDefinition])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "quality.helpfulness", resp.Items[0].EvaluatorID)
				assert.Equal(t, "llm_judge", resp.Items[0].Kind)
				assert.Equal(t, "gpt-4o", resp.Items[0].Config["model"])
				require.Len(t, resp.Items[0].OutputKeys, 1)
				require.NotNil(t, resp.Items[0].OutputKeys[0].PassThreshold)
				assert.Equal(t, 0.7, *resp.Items[0].OutputKeys[0].PassThreshold)
				assert.Equal(t, "42", resp.NextCursor)
			},
		},
		{
			name:   "list evaluators passes limit and cursor through",
			params: Agento11yEvalsReadParams{Operation: "list_evaluators", Limit: 25, Cursor: "100"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "25", r.URL.Query().Get("limit"))
				require.Equal(t, "100", r.URL.Query().Get("cursor"))

				writeJSONResponse(t, w, `{"items":[]}`)
			},
		},
		{
			name:   "get evaluator",
			params: Agento11yEvalsReadParams{Operation: "get_evaluator", EvaluatorID: "quality.helpfulness"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/evaluators/quality.helpfulness", r.URL.Path)

				writeJSONResponse(t, w, `{
					"evaluator_id": "quality.helpfulness",
					"version": "v2",
					"kind": "llm_judge",
					"description": "scores helpfulness",
					"config": {"prompt": "is this helpful?"},
					"output_keys": [{"key": "helpfulness", "type": "number"}],
					"source_template_id": "template.helpfulness"
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				evaluator, ok := result.(*Agento11yEvaluatorDefinition)
				require.True(t, ok)
				assert.Equal(t, "quality.helpfulness", evaluator.EvaluatorID)
				assert.Equal(t, "v2", evaluator.Version)
				assert.Equal(t, "llm_judge", evaluator.Kind)
				assert.Equal(t, "is this helpful?", evaluator.Config["prompt"])
				assert.Equal(t, "template.helpfulness", evaluator.SourceTemplateID)
				require.Len(t, evaluator.OutputKeys, 1)
				assert.Equal(t, "helpfulness", evaluator.OutputKeys[0].Key)
			},
		},
		{
			name:   "list templates preserves scope and pagination",
			params: Agento11yEvalsReadParams{Operation: "list_templates", Scope: "tenant", Limit: 25, Cursor: "100"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/templates", r.URL.Path)
				require.Equal(t, "tenant", r.URL.Query().Get("scope"))
				require.Equal(t, "25", r.URL.Query().Get("limit"))
				require.Equal(t, "100", r.URL.Query().Get("cursor"))

				writeJSONResponse(t, w, `{"items":[{
					"template_id": "template.helpfulness",
					"scope": "tenant",
					"kind": "llm_judge",
					"latest_version": "v3"
				}],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yTemplateDefinition])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "template.helpfulness", resp.Items[0].TemplateID)
				assert.Equal(t, "tenant", resp.Items[0].Scope)
				assert.Equal(t, "v3", resp.Items[0].LatestVersion)
				assert.Empty(t, resp.NextCursor)
			},
		},
		{
			name:   "list templates without scope omits the filter",
			params: Agento11yEvalsReadParams{Operation: "list_templates"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.False(t, r.URL.Query().Has("scope"))
				require.Equal(t, "50", r.URL.Query().Get("limit"))

				writeJSONResponse(t, w, `{"items":[]}`)
			},
		},
		{
			name:   "get template is returned untyped",
			params: Agento11yEvalsReadParams{Operation: "get_template", TemplateID: "template.helpfulness"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/templates/template.helpfulness", r.URL.Path)

				writeJSONResponse(t, w, `{
					"template_id": "template.helpfulness",
					"kind": "llm_judge",
					"config": {"prompt": "is this helpful?"},
					"output_keys": [{"key": "helpfulness", "type": "number"}],
					"versions": [{"version": "v1"}, {"version": "v2"}]
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				detail, ok := result.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "template.helpfulness", detail["template_id"])
				assert.Len(t, detail["versions"], 2)
				assert.NotNil(t, detail["config"])
			},
		},
		{
			name:   "list template versions",
			params: Agento11yEvalsReadParams{Operation: "list_template_versions", TemplateID: "template.helpfulness"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/templates/template.helpfulness/versions", r.URL.Path)
				require.Equal(t, "50", r.URL.Query().Get("limit"))

				writeJSONResponse(t, w, `{"items":[{
					"template_id": "template.helpfulness",
					"version": "v2",
					"config": {"prompt": "is this helpful?"},
					"output_keys": [{"key": "helpfulness", "type": "number"}],
					"changelog": "tightened the rubric"
				}],"next_cursor":"7"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yTemplateVersion])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "v2", resp.Items[0].Version)
				assert.Equal(t, "tightened the rubric", resp.Items[0].Changelog)
				assert.Equal(t, "7", resp.NextCursor)
			},
		},
		{
			name:   "list judge providers decodes the providers envelope",
			params: Agento11yEvalsReadParams{Operation: "list_judge_providers"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/judge/providers", r.URL.Path)
				require.Empty(t, r.URL.RawQuery)

				writeJSONResponse(t, w, `{"providers":[{"id":"openai","name":"OpenAI","type":"openai"}]}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11yJudgeProvidersResponse)
				require.True(t, ok)
				require.Len(t, resp.Providers, 1)
				assert.Equal(t, "openai", resp.Providers[0].ID)
				assert.Equal(t, "OpenAI", resp.Providers[0].Name)
			},
		},
		{
			name:   "list judge models preserves the provider filter",
			params: Agento11yEvalsReadParams{Operation: "list_judge_models", Provider: "openai"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/judge/models", r.URL.Path)
				require.Equal(t, "openai", r.URL.Query().Get("provider"))
				require.False(t, r.URL.Query().Has("limit"))

				writeJSONResponse(t, w, `{"models":[{"id":"gpt-4o","name":"GPT-4o","provider":"openai","context_window":128000}]}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11yJudgeModelsResponse)
				require.True(t, ok)
				require.Len(t, resp.Models, 1)
				assert.Equal(t, "gpt-4o", resp.Models[0].ID)
				assert.Equal(t, 128000, resp.Models[0].ContextWindow)
			},
		},
		{
			name:    "get evaluator without evaluator_id",
			params:  Agento11yEvalsReadParams{Operation: "get_evaluator"},
			wantErr: "evaluator_id is required",
		},
		{
			name:    "get template without template_id",
			params:  Agento11yEvalsReadParams{Operation: "get_template"},
			wantErr: "template_id is required",
		},
		{
			name:    "list template versions without template_id",
			params:  Agento11yEvalsReadParams{Operation: "list_template_versions"},
			wantErr: "template_id is required",
		},
		{
			name:    "write operation is rejected by the read variant",
			params:  Agento11yEvalsReadParams{Operation: "upsert_evaluator"},
			wantErr: "unknown operation",
		},
		{
			name:    "unknown operation",
			params:  Agento11yEvalsReadParams{Operation: "list"},
			wantErr: "unknown operation",
		},
		{
			name:   "permission error surfaces the plugin body",
			params: Agento11yEvalsReadParams{Operation: "list_evaluators"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`permission denied: grafana-agento11y-app.data:read required`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403: permission denied: grafana-agento11y-app.data:read required",
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

			result, err := agento11yEvalsRead(ctx, tc.params)
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

func TestAgento11yEvalsReadEvalRules(t *testing.T) {
	testCases := []struct {
		name        string
		params      Agento11yEvalsReadParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list rules sends the default limit",
			params: Agento11yEvalsReadParams{Operation: "list_rules"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/rules", r.URL.Path)
				require.Equal(t, "50", r.URL.Query().Get("limit"))

				writeJSONResponse(t, w, `{"items":[{
					"rule_id": "my.rule",
					"enabled": true,
					"selector": "user_visible_turn",
					"sample_rate": 0.25,
					"evaluator_ids": ["quality.helpfulness"],
					"match": {"agent_name": ["my-agent"]}
				}],"next_cursor":"3"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yRuleDefinition])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				rule := resp.Items[0]
				assert.Equal(t, "my.rule", rule.RuleID)
				assert.True(t, rule.Enabled)
				assert.Equal(t, "user_visible_turn", rule.Selector)
				assert.Equal(t, 0.25, rule.SampleRate)
				assert.Equal(t, []string{"quality.helpfulness"}, rule.EvaluatorIDs)
				assert.NotNil(t, rule.Match["agent_name"])
				assert.Equal(t, "3", resp.NextCursor)
			},
		},
		{
			name:   "get rule",
			params: Agento11yEvalsReadParams{Operation: "get_rule", RuleID: "my.rule"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/rules/my.rule", r.URL.Path)

				writeJSONResponse(t, w, `{
					"rule_id": "my.rule",
					"enabled": true,
					"selector": "conversation",
					"sample_rate": 1,
					"evaluator_ids": ["quality.helpfulness"],
					"min_idle_seconds": 300
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				rule, ok := result.(*Agento11yRuleDefinition)
				require.True(t, ok)
				assert.Equal(t, "conversation", rule.Selector)
				require.NotNil(t, rule.MinIdleSeconds)
				assert.Equal(t, 300, *rule.MinIdleSeconds)
			},
		},
		{
			name:   "list guards reads hook-rules",
			params: Agento11yEvalsReadParams{Operation: "list_guards", Limit: 10, Cursor: "5"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/hook-rules", r.URL.Path)
				require.NotContains(t, r.URL.Path, "/eval/guards")
				require.Equal(t, "10", r.URL.Query().Get("limit"))
				require.Equal(t, "5", r.URL.Query().Get("cursor"))

				writeJSONResponse(t, w, `{"items":[{
					"rule_id": "guard.safety",
					"enabled": true,
					"phase": "preflight",
					"priority": 10,
					"selector": "all",
					"action_on_fail": "warn",
					"short_circuit": false,
					"redact": {"patterns": [{"id": "ssn", "regex": "\\d{3}-\\d{2}-\\d{4}"}]}
				}],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yHookRuleDefinition])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				guard := resp.Items[0]
				assert.Equal(t, "guard.safety", guard.RuleID)
				assert.Equal(t, "preflight", guard.Phase)
				assert.Equal(t, "warn", guard.ActionOnFail)
				require.NotNil(t, guard.Redact)
				require.Len(t, guard.Redact.Patterns, 1)
				assert.Equal(t, "ssn", guard.Redact.Patterns[0].ID)
				assert.Equal(t, `\d{3}-\d{2}-\d{4}`, guard.Redact.Patterns[0].Regex)
			},
		},
		{
			name:   "get guard reads one hook-rule",
			params: Agento11yEvalsReadParams{Operation: "get_guard", RuleID: "guard.safety"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/hook-rules/guard.safety", r.URL.Path)

				writeJSONResponse(t, w, `{
					"rule_id": "guard.safety",
					"enabled": true,
					"phase": "postflight",
					"action_on_fail": "deny",
					"tool_filter": {"blocked_names": ["shell_exec"]}
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				guard, ok := result.(*Agento11yHookRuleDefinition)
				require.True(t, ok)
				assert.Equal(t, "postflight", guard.Phase)
				assert.Equal(t, "deny", guard.ActionOnFail)
				require.NotNil(t, guard.ToolFilter)
				assert.Equal(t, []string{"shell_exec"}, guard.ToolFilter.BlockedNames)
			},
		},
		{
			name:    "get rule without rule_id",
			params:  Agento11yEvalsReadParams{Operation: "get_rule"},
			wantErr: "rule_id is required",
		},
		{
			name:    "get guard without rule_id",
			params:  Agento11yEvalsReadParams{Operation: "get_guard"},
			wantErr: "rule_id is required",
		},
		{
			name:    "write operation is rejected by the read variant",
			params:  Agento11yEvalsReadParams{Operation: "create_rule"},
			wantErr: "unknown operation",
		},
		{
			name:    "unknown operation",
			params:  Agento11yEvalsReadParams{Operation: "list"},
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

			result, err := agento11yEvalsRead(ctx, tc.params)
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

// writeJSONResponse writes a JSON body from a mock plugin handler.
func writeJSONResponse(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write([]byte(body))
	require.NoError(t, err)
}

func TestAgento11yEvalsWriteEvaluators(t *testing.T) {
	testCases := []struct {
		name        string
		params      Agento11yEvalsWriteParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name: "upsert evaluator posts the definition with the ID in the body",
			params: Agento11yEvalsWriteParams{
				Operation: "upsert_evaluator",
				Definition: map[string]any{
					"evaluator_id": "quality.helpfulness",
					"version":      "v2",
					"kind":         "llm_judge",
					"config":       map[string]any{"prompt": "is this helpful?"},
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/evaluators", r.URL.Path)
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))

				body := decodeRequestBody(t, r)
				assert.Equal(t, "quality.helpfulness", body["evaluator_id"])
				assert.Equal(t, "v2", body["version"])
				assert.Equal(t, "llm_judge", body["kind"])

				writeJSONResponse(t, w, `{"evaluator_id":"quality.helpfulness","version":"v2","kind":"llm_judge"}`)
			},
			checkResult: func(t *testing.T, result any) {
				evaluator, ok := result.(*Agento11yEvaluatorDefinition)
				require.True(t, ok)
				assert.Equal(t, "quality.helpfulness", evaluator.EvaluatorID)
				assert.Equal(t, "v2", evaluator.Version)
			},
		},
		{
			name: "delete evaluator handles 204 with an empty body",
			params: Agento11yEvalsWriteParams{
				Operation:   "delete_evaluator",
				EvaluatorID: "quality.helpfulness",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/evaluators/quality.helpfulness", r.URL.Path)

				w.WriteHeader(http.StatusNoContent)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Contains(t, message, "quality.helpfulness")
				assert.Contains(t, message, "deleted successfully")
			},
		},
		{
			name: "fork template keeps the :fork action suffix unescaped",
			params: Agento11yEvalsWriteParams{
				Operation:  "fork_template",
				TemplateID: "template.helpfulness",
				Definition: map[string]any{"evaluator_id": "quality.helpfulness"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/templates/template.helpfulness:fork", r.URL.Path)
				require.Contains(t, r.RequestURI, ":fork", "the colon before fork must not be percent-encoded")

				body := decodeRequestBody(t, r)
				assert.Equal(t, "quality.helpfulness", body["evaluator_id"])

				writeJSONResponse(t, w, `{"evaluator_id":"quality.helpfulness","version":"v3","kind":"llm_judge","source_template_id":"template.helpfulness"}`)
			},
			checkResult: func(t *testing.T, result any) {
				evaluator, ok := result.(*Agento11yEvaluatorDefinition)
				require.True(t, ok)
				assert.Equal(t, "template.helpfulness", evaluator.SourceTemplateID)
				assert.Equal(t, "v3", evaluator.Version)
			},
		},
		{
			name: "test evaluator posts to the resources-root action",
			params: Agento11yEvalsWriteParams{
				Operation:    "test_evaluator",
				GenerationID: "gen-123",
				Definition: map[string]any{
					"kind":        "regex",
					"config":      map[string]any{"pattern": "hello"},
					"output_keys": []any{map[string]any{"key": "matched", "type": "bool"}},
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval:test", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "gen-123", body["generation_id"])
				assert.Equal(t, "regex", body["kind"])
				assert.NotContains(t, body, "evaluator_id", "the definition of a validated test_evaluator call carries no evaluator id")

				writeJSONResponse(t, w, `{
					"generation_id": "gen-123",
					"conversation_id": "conv-1",
					"scores": [{"key":"matched","type":"bool","value":true,"passed":true,"explanation":"found"}],
					"execution_time_ms": 812
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11yEvalTestResponse)
				require.True(t, ok)
				assert.Equal(t, "gen-123", resp.GenerationID)
				assert.Equal(t, "conv-1", resp.ConversationID)
				assert.Equal(t, int64(812), resp.ExecutionTimeMs)
				require.Len(t, resp.Scores, 1)
				assert.Equal(t, "matched", resp.Scores[0].Key)
				require.NotNil(t, resp.Scores[0].Passed)
				assert.True(t, *resp.Scores[0].Passed)
			},
		},
		{
			// Under the disjoint-operation split, agento11y_evals_write no
			// longer reaches the read dispatch for a sibling read operation -
			// see TestAgento11yEvalsWriteRejectsReadOperations below for the
			// general case across every domain.
			name:    "a read operation is rejected, not dispatched",
			params:  Agento11yEvalsWriteParams{Operation: "list_evaluators"},
			wantErr: `"list_evaluators" is an agento11y_evals_read operation, not agento11y_evals_write`,
		},
		{
			name: "missing eval:write permission surfaces the plugin body",
			params: Agento11yEvalsWriteParams{
				Operation:  "upsert_evaluator",
				Definition: map[string]any{"evaluator_id": "quality.helpfulness"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`permission denied: grafana-agento11y-app.eval:write required`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403: permission denied: grafana-agento11y-app.eval:write required",
		},
		{
			name: "version conflict surfaces the 409 body",
			params: Agento11yEvalsWriteParams{
				Operation:  "upsert_evaluator",
				Definition: map[string]any{"evaluator_id": "quality.helpfulness", "version": "v1"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, err := w.Write([]byte(`evaluator version "v1" already exists`))
				require.NoError(t, err)
			},
			wantErr: `request failed with status 409: evaluator version "v1" already exists`,
		},
		{
			name:    "upsert without definition",
			params:  Agento11yEvalsWriteParams{Operation: "upsert_evaluator"},
			wantErr: "definition is required for 'upsert_evaluator'",
		},
		{
			name: "upsert without an evaluator_id in the definition",
			params: Agento11yEvalsWriteParams{
				Operation:  "upsert_evaluator",
				Definition: map[string]any{"kind": "regex", "version": "v1"},
			},
			wantErr: `definition.evaluator_id (a non-empty string) is required for "upsert_evaluator" operation`,
		},
		{
			name: "upsert rejects an evaluator_id parameter that disagrees with the definition",
			params: Agento11yEvalsWriteParams{
				Operation:   "upsert_evaluator",
				EvaluatorID: "from.param",
				Definition:  map[string]any{"evaluator_id": "from.definition", "kind": "regex"},
			},
			wantErr: `evaluator_id "from.param" conflicts with definition.evaluator_id "from.definition"`,
		},
		{
			name: "upsert accepts an evaluator_id parameter that matches the definition",
			params: Agento11yEvalsWriteParams{
				Operation:   "upsert_evaluator",
				EvaluatorID: "quality.helpfulness",
				Definition:  map[string]any{"evaluator_id": "quality.helpfulness", "kind": "regex"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/evaluators", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "quality.helpfulness", body["evaluator_id"])

				writeJSONResponse(t, w, `{"evaluator_id":"quality.helpfulness","kind":"regex"}`)
			},
		},
		{
			name:    "delete without evaluator_id",
			params:  Agento11yEvalsWriteParams{Operation: "delete_evaluator"},
			wantErr: "evaluator_id is required for 'delete_evaluator'",
		},
		{
			name:    "fork without template_id",
			params:  Agento11yEvalsWriteParams{Operation: "fork_template", Definition: map[string]any{"evaluator_id": "x"}},
			wantErr: "template_id is required for 'fork_template'",
		},
		{
			name: "fork without definition",
			params: Agento11yEvalsWriteParams{
				Operation:  "fork_template",
				TemplateID: "template.helpfulness",
			},
			wantErr: "definition is required for 'fork_template'",
		},
		{
			name: "fork without an evaluator_id in the definition",
			params: Agento11yEvalsWriteParams{
				Operation:  "fork_template",
				TemplateID: "template.helpfulness",
				Definition: map[string]any{"version": "v2"},
			},
			wantErr: `definition.evaluator_id (a non-empty string) is required for "fork_template" operation`,
		},
		{
			name:    "test without generation_id",
			params:  Agento11yEvalsWriteParams{Operation: "test_evaluator", Definition: map[string]any{"kind": "regex"}},
			wantErr: "generation_id is required for 'test_evaluator'",
		},
		{
			name: "test rejects a definition carrying evaluator_id",
			params: Agento11yEvalsWriteParams{
				Operation:    "test_evaluator",
				GenerationID: "gen-123",
				Definition:   map[string]any{"evaluator_id": "quality.helpfulness", "kind": "regex"},
			},
			wantErr: `definition must not contain "evaluator_id" for 'test_evaluator' operation`,
		},
		{
			name:    "unknown operation lists the write operations",
			params:  Agento11yEvalsWriteParams{Operation: "create_evaluator"},
			wantErr: "unknown operation \"create_evaluator\", must be one of: " + agento11yEvalsWriteOperations,
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

			result, err := agento11yEvalsWrite(ctx, tc.params)
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

func TestAgento11yEvalsWriteEvalRules(t *testing.T) {
	testCases := []struct {
		name        string
		params      Agento11yEvalsWriteParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name: "create rule",
			params: Agento11yEvalsWriteParams{
				Operation: "create_rule",
				Definition: map[string]any{
					"rule_id":       "my.rule",
					"enabled":       true,
					"selector":      "user_visible_turn",
					"sample_rate":   0.5,
					"evaluator_ids": []any{"quality.helpfulness"},
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/rules", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "my.rule", body["rule_id"])
				assert.Equal(t, "user_visible_turn", body["selector"])

				writeJSONResponse(t, w, `{"rule_id":"my.rule","enabled":true,"selector":"user_visible_turn","sample_rate":0.5,"evaluator_ids":["quality.helpfulness"]}`)
			},
			checkResult: func(t *testing.T, result any) {
				rule, ok := result.(*Agento11yRuleDefinition)
				require.True(t, ok)
				assert.Equal(t, "my.rule", rule.RuleID)
				assert.Equal(t, 0.5, rule.SampleRate)
			},
		},
		{
			name: "update rule patches without a rule_id key",
			params: Agento11yEvalsWriteParams{
				Operation: "update_rule",
				RuleID:    "my.rule",
				Definition: map[string]any{
					"rule_id":     "my.rule",
					"sample_rate": 0.1,
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPatch, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/rules/my.rule", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.NotContains(t, body, "rule_id", "the plugin rejects rule_id in a PATCH body")
				assert.Equal(t, 0.1, body["sample_rate"])

				writeJSONResponse(t, w, `{"rule_id":"my.rule","enabled":true,"sample_rate":0.1}`)
			},
			checkResult: func(t *testing.T, result any) {
				rule, ok := result.(*Agento11yRuleDefinition)
				require.True(t, ok)
				assert.Equal(t, 0.1, rule.SampleRate)
			},
		},
		{
			name: "delete rule handles 204 with an empty body",
			params: Agento11yEvalsWriteParams{
				Operation: "delete_rule",
				RuleID:    "my.rule",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/rules/my.rule", r.URL.Path)

				w.WriteHeader(http.StatusNoContent)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Contains(t, message, "my.rule")
			},
		},
		{
			name: "preview rule uses the action endpoint",
			params: Agento11yEvalsWriteParams{
				Operation: "preview_rule",
				Definition: map[string]any{
					"selector":    "user_visible_turn",
					"match":       map[string]any{"agent_name": []any{"my-agent"}},
					"sample_rate": 0.25,
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/rules:preview", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "user_visible_turn", body["selector"])
				assert.Equal(t, 0.25, body["sample_rate"])
				assert.NotNil(t, body["match"])

				writeJSONResponse(t, w, `{
					"window_hours": 24,
					"total_generations": 400,
					"scanned_generations": 400,
					"matching_generations": 40,
					"sampled_generations": 10,
					"samples": [{"generation_id":"gen-1","conversation_id":"conv-1","agent_name":"my-agent","created_at":"2026-04-23T10:00:00Z"}]
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11yRulePreviewResponse)
				require.True(t, ok)
				assert.Equal(t, 24, resp.WindowHours)
				assert.Equal(t, 40, resp.MatchingGenerations)
				assert.Equal(t, 10, resp.SampledGenerations)
				require.Len(t, resp.Samples, 1)
				assert.Equal(t, "gen-1", resp.Samples[0].GenerationID)
			},
		},
		{
			name: "create guard posts to hook-rules",
			params: Agento11yEvalsWriteParams{
				Operation: "create_guard",
				Definition: map[string]any{
					"rule_id":        "guard.safety",
					"action_on_fail": "warn",
					"redact":         map[string]any{"patterns": []any{map[string]any{"id": "ssn", "regex": `\d{3}-\d{2}-\d{4}`}}},
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/hook-rules", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "guard.safety", body["rule_id"])
				assert.Equal(t, "warn", body["action_on_fail"])

				writeJSONResponse(t, w, `{"rule_id":"guard.safety","enabled":true,"phase":"preflight","selector":"all","action_on_fail":"warn","redact":{"patterns":[{"id":"ssn","regex":"x"}]}}`)
			},
			checkResult: func(t *testing.T, result any) {
				guard, ok := result.(*Agento11yHookRuleDefinition)
				require.True(t, ok)
				assert.Equal(t, "guard.safety", guard.RuleID)
				assert.Equal(t, "warn", guard.ActionOnFail)
				require.NotNil(t, guard.Redact)
			},
		},
		{
			name: "update guard is a full replace with PUT",
			params: Agento11yEvalsWriteParams{
				Operation: "update_guard",
				RuleID:    "guard.safety",
				Definition: map[string]any{
					"rule_id":        "guard.safety",
					"enabled":        true,
					"phase":          "preflight",
					"priority":       10,
					"selector":       "all",
					"action_on_fail": "deny",
					"evaluator_ids":  []any{"policy.injection"},
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPut, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/hook-rules/guard.safety", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "guard.safety", body["rule_id"], "a full replace keeps rule_id in the body")
				assert.Equal(t, "deny", body["action_on_fail"])

				writeJSONResponse(t, w, `{"rule_id":"guard.safety","enabled":true,"phase":"preflight","priority":10,"selector":"all","action_on_fail":"deny","evaluator_ids":["policy.injection"]}`)
			},
			checkResult: func(t *testing.T, result any) {
				guard, ok := result.(*Agento11yHookRuleDefinition)
				require.True(t, ok)
				assert.Equal(t, "deny", guard.ActionOnFail)
				assert.Equal(t, []string{"policy.injection"}, guard.EvaluatorIDs)
			},
		},
		{
			name: "delete guard deletes a hook-rule",
			params: Agento11yEvalsWriteParams{
				Operation: "delete_guard",
				RuleID:    "guard.safety",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/hook-rules/guard.safety", r.URL.Path)

				w.WriteHeader(http.StatusNoContent)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Contains(t, message, "guard.safety")
			},
		},
		{
			// Under the disjoint-operation split, agento11y_evals_write no
			// longer reaches the read dispatch for a sibling read operation -
			// see TestAgento11yEvalsWriteRejectsReadOperations below for the
			// general case across every domain.
			name:    "a read operation is rejected, not dispatched",
			params:  Agento11yEvalsWriteParams{Operation: "list_guards"},
			wantErr: `"list_guards" is an agento11y_evals_read operation, not agento11y_evals_write`,
		},
		{
			name: "rejected redact shape surfaces the 400 body",
			params: Agento11yEvalsWriteParams{
				Operation: "create_guard",
				Definition: map[string]any{
					"rule_id": "guard.safety",
					"redact":  map[string]any{"patterns": []any{map[string]any{"id": "ssn", "regex": "x", "replacement": "[redacted]"}}},
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, err := w.Write([]byte(`invalid request body: unknown field "replacement"`))
				require.NoError(t, err)
			},
			wantErr: `request failed with status 400: invalid request body: unknown field "replacement"`,
		},
		{
			name:    "create rule without definition",
			params:  Agento11yEvalsWriteParams{Operation: "create_rule"},
			wantErr: `definition is required for "create_rule"`,
		},
		{
			name:    "update rule without rule_id",
			params:  Agento11yEvalsWriteParams{Operation: "update_rule", Definition: map[string]any{"enabled": false}},
			wantErr: `rule_id is required for "update_rule"`,
		},
		{
			name: "update guard without definition",
			params: Agento11yEvalsWriteParams{
				Operation: "update_guard",
				RuleID:    "guard.safety",
			},
			wantErr: `definition is required for "update_guard"`,
		},
		{
			name:    "delete guard without rule_id",
			params:  Agento11yEvalsWriteParams{Operation: "delete_guard"},
			wantErr: `rule_id is required for "delete_guard"`,
		},
		{
			name:    "unknown operation lists the write operations",
			params:  Agento11yEvalsWriteParams{Operation: "upsert_rule"},
			wantErr: "unknown operation \"upsert_rule\", must be one of: " + agento11yEvalsWriteOperations,
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

			result, err := agento11yEvalsWrite(ctx, tc.params)
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

// decodeRequestBody decodes a JSON request body received by the mock plugin.
func decodeRequestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	return body
}

// TestAddAgento11yToolsRegistration checks the registration contract that
// --disable-write relies on for the split-name shape: agento11y_read and
// agento11y_evals_read are registered unconditionally, agento11y_evals_write
// is registered if and only if write tools are enabled (never re-registered
// under the same name with a narrower schema, unlike the pre-consolidation
// single-name dual-schema tools), and no tool name is registered twice.
func TestAddAgento11yToolsRegistration(t *testing.T) {
	for _, tc := range []struct {
		name             string
		enableWriteTools bool
	}{
		{name: "write tools enabled", enableWriteTools: true},
		{name: "write tools disabled", enableWriteTools: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			advertised := listAgento11yTools(t, tc.enableWriteTools)

			for _, name := range []string{"agento11y_read", "agento11y_evals_read"} {
				_, ok := advertised[name]
				assert.True(t, ok, "%s should be registered whether or not write tools are enabled", name)
			}

			_, hasWrite := advertised["agento11y_evals_write"]
			assert.Equal(t, tc.enableWriteTools, hasWrite, "agento11y_evals_write should be registered only when write tools are enabled")

			// agento11y_read has no write half at all: it must never appear
			// under a "_write" name.
			_, hasReadWrite := advertised["agento11y_write"]
			assert.False(t, hasReadWrite, "agento11y_read has no write counterpart")
		})
	}
}

// TestAgento11yEvalsSplitOperationsAreDisjoint checks the core invariant the
// split-name convention exists to guarantee: agento11y_evals_read and
// agento11y_evals_write advertise non-overlapping operation enums, so
// name-based write-tool classification (as grafana-assistant-app's
// discoverWriteTools does) never has to guess, and neither tool's schema
// silently grants the other's capability.
func TestAgento11yEvalsSplitOperationsAreDisjoint(t *testing.T) {
	advertised := listAgento11yTools(t, true)

	readTool, ok := advertised["agento11y_evals_read"]
	require.True(t, ok)
	writeTool, ok := advertised["agento11y_evals_write"]
	require.True(t, ok)

	require.NotEmpty(t, readTool.operations)
	require.NotEmpty(t, writeTool.operations)

	for _, op := range writeTool.operations {
		assert.NotContains(t, readTool.operations, op, "agento11y_evals_write's %q operation must not also be one of agento11y_evals_read's", op)
	}
	for _, op := range readTool.operations {
		assert.NotContains(t, writeTool.operations, op, "agento11y_evals_read's %q operation must not also be one of agento11y_evals_write's", op)
	}

	// A model that cannot see the required role reads a 403 as a broken tool.
	assert.Contains(t, writeTool.description, "grafana-agento11y-app.eval:write", "agento11y_evals_write should name the permission its writes need")
}

// TestAgento11yEvalsWriteRejectsReadOperations is the write-side half of the
// disjoint-operation contract at the validation layer: every operation
// agento11y_evals_read owns must be rejected by Agento11yEvalsWriteParams with
// a specific "call agento11y_evals_read instead" error, never accepted. This
// is TestDelegation_SplitNameDisjointOperations's pattern
// (tools/operation_validation_test.go) exercised against the real domain.
func TestAgento11yEvalsWriteRejectsReadOperations(t *testing.T) {
	for _, op := range strings.Split(agento11yEvalsReadOperations, ", ") {
		t.Run(op, func(t *testing.T) {
			err := Agento11yEvalsWriteParams{Operation: op}.validate()
			require.Error(t, err, "agento11y_evals_write must never silently accept a read operation")
			assert.Contains(t, err.Error(), "agento11y_evals_read operation, not agento11y_evals_write")
		})
	}
}

// TestAgento11yEvalsReadRejectsWriteOperations is the read-side half: every
// operation agento11y_evals_write owns is genuinely unknown to
// Agento11yEvalsReadParams, since the read tool has no sibling-aware
// delegation to a write tool it doesn't implement.
func TestAgento11yEvalsReadRejectsWriteOperations(t *testing.T) {
	for _, op := range strings.Split(agento11yEvalsWriteOperations, ", ") {
		t.Run(op, func(t *testing.T) {
			err := Agento11yEvalsReadParams{Operation: op}.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown operation")
		})
	}
}

// TestAgento11yEvalsAdvertisedOperationsAreImplemented catches drift between
// an enum= jsonschema tag and the switch statements that dispatch it: every
// operation agento11y_evals_read/_write advertises must validate without an
// "unknown operation" error (a missing required field is fine and expected).
func TestAgento11yEvalsAdvertisedOperationsAreImplemented(t *testing.T) {
	advertised := listAgento11yTools(t, true)

	for _, op := range advertised["agento11y_evals_read"].operations {
		err := Agento11yEvalsReadParams{Operation: op}.validate()
		if err != nil {
			assert.NotContains(t, err.Error(), "unknown operation", "agento11y_evals_read advertises %q but does not accept it", op)
		}
	}
	for _, op := range advertised["agento11y_evals_write"].operations {
		err := Agento11yEvalsWriteParams{Operation: op}.validate()
		if err != nil {
			assert.NotContains(t, err.Error(), "unknown operation", "agento11y_evals_write advertises %q but does not accept it", op)
		}
	}
}

// agento11yAdvertisedTool is one agento11y tool as a client sees it in tools/list.
type agento11yAdvertisedTool struct {
	operations  []string
	schema      string // the raw input schema, descriptions included
	description string
}

// listAgento11yTools registers the Agent Observability category and returns
// the advertised tools, failing if a tool name is registered more than once.
func listAgento11yTools(t *testing.T, enableWriteTools bool) map[string]agento11yAdvertisedTool {
	t.Helper()

	srv := server.NewMCPServer("test", "0")
	AddAgento11yTools(srv, enableWriteTools)

	response := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	raw, err := json.Marshal(response)
	require.NoError(t, err)

	var listed struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &listed))

	tools := map[string]agento11yAdvertisedTool{}
	for _, tool := range listed.Result.Tools {
		switch tool.Name {
		case "agento11y_read", "agento11y_evals_read", "agento11y_evals_write":
		default:
			continue
		}
		_, duplicate := tools[tool.Name]
		require.False(t, duplicate, "%s was registered more than once", tool.Name)

		var schema struct {
			Properties struct {
				Operation struct {
					Enum []string `json:"enum"`
				} `json:"operation"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(tool.InputSchema, &schema))

		tools[tool.Name] = agento11yAdvertisedTool{
			operations:  schema.Properties.Operation.Enum,
			schema:      string(tool.InputSchema),
			description: tool.Description,
		}
	}
	return tools
}
