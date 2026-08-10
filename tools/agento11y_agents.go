package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

// Agent catalog of Agent Observability (/query/agents on the
// grafana-agento11y-app plugin resources proxy). The catalog is derived from
// ingested telemetry: an agent appears once its generations have been seen, and
// its generations are grouped by effective version, which the plugin takes from
// the version the SDK reported, else a hash of the declared agent version, else
// a hash of the system prompt.

// agento11yEffectiveVersionPattern mirrors the server-side effective version
// shape: a "sha256:" prefix followed by a 64-character lowercase hex digest.
// Declared versions such as "1.4.2" are output-only and rejected as input.
var agento11yEffectiveVersionPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// agento11yRelativeTimeBound reports whether a time bound re-resolves against
// the wall clock, which makes it drift between two calls. The order mirrors how
// the time parser reads the string: an integer is an absolute epoch in
// milliseconds, a "now" expression is relative, and so is a bare duration such
// as "7d" or "30m", which is easy to mistake for an absolute value.
func agento11yRelativeTimeBound(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return false
	}
	if strings.HasPrefix(value, "now") {
		return true
	}
	_, err := gtime.ParseDuration(value)
	return err == nil
}

// Agento11yAgentTokenEstimate is the token cost of one agent version, split
// between its system prompt and its tool definitions.
type Agento11yAgentTokenEstimate struct {
	SystemPrompt int `json:"system_prompt"`
	ToolsTotal   int `json:"tools_total"`
	Total        int `json:"total"`
}

// Agento11yAgent is a row from GET /query/agents, one per agent name.
// LatestDeclaredVersion is a pointer because an agent whose telemetry never
// carried a declared version omits the field entirely.
type Agento11yAgent struct {
	AgentName              string                      `json:"agent_name"`
	LatestEffectiveVersion string                      `json:"latest_effective_version"`
	LatestDeclaredVersion  *string                     `json:"latest_declared_version,omitempty"`
	FirstSeenAt            time.Time                   `json:"first_seen_at,omitzero"`
	LatestSeenAt           time.Time                   `json:"latest_seen_at,omitzero"`
	GenerationCount        int64                       `json:"generation_count"`
	VersionCount           int                         `json:"version_count"`
	ToolCount              int                         `json:"tool_count"`
	SystemPromptPrefix     string                      `json:"system_prompt_prefix"`
	TokenEstimate          Agento11yAgentTokenEstimate `json:"token_estimate"`
}

// Agento11yAgentVersion is a row from GET /query/agents/versions, one per
// effective version of a single agent.
type Agento11yAgentVersion struct {
	EffectiveVersion      string                      `json:"effective_version"`
	DeclaredVersionFirst  *string                     `json:"declared_version_first,omitempty"`
	DeclaredVersionLatest *string                     `json:"declared_version_latest,omitempty"`
	FirstSeenAt           time.Time                   `json:"first_seen_at,omitzero"`
	LastSeenAt            time.Time                   `json:"last_seen_at,omitzero"`
	GenerationCount       int64                       `json:"generation_count"`
	ToolCount             int                         `json:"tool_count"`
	SystemPromptPrefix    string                      `json:"system_prompt_prefix"`
	TokenEstimate         Agento11yAgentTokenEstimate `json:"token_estimate"`
}

// Agento11yAgentVersionScore is a row from GET /query/agents/version-scores:
// the evaluation score aggregates of one effective version.
type Agento11yAgentVersionScore struct {
	EffectiveVersion string                                  `json:"effective_version"`
	DeclaredVersion  *string                                 `json:"declared_version,omitempty"`
	Evaluators       []Agento11yAgentVersionEvaluatorSummary `json:"evaluators"`
	TotalScores      int                                     `json:"total_scores"`
	PassCount        int                                     `json:"pass_count"`
	FailCount        int                                     `json:"fail_count"`
	FirstSeenAt      time.Time                               `json:"first_seen_at,omitzero"`
	LastSeenAt       time.Time                               `json:"last_seen_at,omitzero"`
}

// Agento11yAgentVersionEvaluatorSummary holds the aggregates of one score key
// within one agent version. MeanScore is a pointer because non-numeric score
// types (bool and string) have no mean and omit the field.
type Agento11yAgentVersionEvaluatorSummary struct {
	ScoreKey    string   `json:"score_key"`
	TotalScores int      `json:"total_scores"`
	PassCount   int      `json:"pass_count"`
	FailCount   int      `json:"fail_count"`
	MeanScore   *float64 `json:"mean_score,omitempty"`
}

// listAgento11yAgents returns a page of the agent catalog. The seen bounds are
// sent as unix epoch seconds, which is what this route expects (the score route
// below takes RFC3339 instead).
func (c *Client) listAgento11yAgents(ctx context.Context, namePrefix string, seenAfter, seenBefore time.Time, limit int, cursor string) (*agento11yListResponse[Agento11yAgent], error) {
	query := agento11yPageQuery(limit, cursor)
	if namePrefix != "" {
		query.Set("name_prefix", namePrefix)
	}
	if !seenAfter.IsZero() {
		query.Set("seen_after", strconv.FormatInt(seenAfter.Unix(), 10))
	}
	if !seenBefore.IsZero() {
		query.Set("seen_before", strconv.FormatInt(seenBefore.Unix(), 10))
	}

	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yAgent]](ctx, c, http.MethodGet, "/query/agents", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// getAgento11yAgent returns one agent version in full, including its system
// prompt and every tool schema. Decoded as map[string]any so new plugin fields
// reach the caller without a code change here.
func (c *Client) getAgento11yAgent(ctx context.Context, name, version string) (map[string]any, error) {
	query := url.Values{}
	// The key is always set: the upstream handler answers 400 when "name" is
	// missing but treats an empty value as the unnamed agent.
	query.Set("name", name)
	if version != "" {
		query.Set("version", version)
	}
	return fetchAgento11yJSON[map[string]any](ctx, c, http.MethodGet, "/query/agents/lookup", query, nil)
}

// listAgento11yAgentVersions returns a page of one agent's version history.
func (c *Client) listAgento11yAgentVersions(ctx context.Context, name string, limit int, cursor string) (*agento11yListResponse[Agento11yAgentVersion], error) {
	query := agento11yPageQuery(limit, cursor)
	query.Set("name", name)

	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yAgentVersion]](ctx, c, http.MethodGet, "/query/agents/versions", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// listAgento11yAgentVersionScores returns per-version score aggregates. This
// route has no pagination and encodes its window as RFC3339, unlike the epoch
// seconds of /query/agents.
func (c *Client) listAgento11yAgentVersionScores(ctx context.Context, name string, from, to time.Time) (*agento11yListResponse[Agento11yAgentVersionScore], error) {
	query := url.Values{}
	query.Set("name", name)
	if !from.IsZero() {
		query.Set("from", from.Format(time.RFC3339))
	}
	if !to.IsZero() {
		query.Set("to", to.Format(time.RFC3339))
	}

	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yAgentVersionScore]](ctx, c, http.MethodGet, "/query/agents/version-scores", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

const agento11yAgentOperations = "list, get, list_versions, list_version_scores"

// ManageAgento11yAgentsParams is the param struct for agento11y_manage_agents.
type ManageAgento11yAgentsParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=list,enum=get,enum=list_versions,enum=list_version_scores,description=The operation to perform: 'list' for the agent catalog\\, 'get' for one agent version in full (system prompt\\, tools\\, models)\\, 'list_versions' for the version history of one agent\\, 'list_version_scores' for evaluation score aggregates per version"`
	// Pointer, so an omitted agent_name stays distinct from an explicit "",
	// which addresses the unnamed agent.
	AgentName  *string `json:"agent_name,omitempty" jsonschema:"description=Agent name from 'list'. Required for 'get'\\, 'list_versions'\\, and 'list_version_scores'. An explicitly empty string selects the unnamed agent (telemetry with no agent name)\\, which 'get' and 'list_versions' accept and 'list_version_scores' rejects."`
	Version    string  `json:"version,omitempty" jsonschema:"description=Effective version 'sha256:<64 lowercase hex>' from 'list' or 'list_versions'\\, accepted only by 'get'. Omit it for the latest version. Declared versions such as '1.4.2' are not accepted here; they are reported in the declared_version fields."`
	NamePrefix string  `json:"name_prefix,omitempty" jsonschema:"description=Agent name filter (for 'list'). Despite the parameter name\\, the API matches it case-insensitively anywhere in the name\\, so 'agent' also returns 'my-agent'."`
	StartTime  string  `json:"start_time,omitempty" jsonschema:"description=Start of the time range in RFC3339 or relative format (e.g. now-7d). For 'list' it keeps agents last seen at or after this time and defaults to no lower bound. For 'list_version_scores' it starts the score window; omitting it does not mean no lower bound\\, because the API then either scopes to the agent's 50 most recent versions or falls back to a 90-day window."`
	EndTime    string  `json:"end_time,omitempty" jsonschema:"description=End of the time range in RFC3339 or relative format (e.g. now). For 'list' it keeps agents last seen at or before this time and defaults to no upper bound. For 'list_version_scores' it ends the score window; passing it without start_time gives a 90-day window ending here."`
	Limit      int     `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, capped at 200 by the API) (for 'list' and 'list_versions')"`
	Cursor     string  `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor\\, echoed back exactly (for 'list' and 'list_versions'). For 'list' also resend the same name_prefix\\, start_time\\, and end_time as the first call using absolute RFC3339 times; the cursor is bound to those filters and a relative value such as now-7d or 7d drifts between calls and is rejected."`
}

func (p ManageAgento11yAgentsParams) validate() error {
	switch p.Operation {
	case "list", "get", "list_versions", "list_version_scores":
	default:
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yAgentOperations)
	}

	// Only the lookup route behind 'get' reads a version. The other three
	// ignore unknown query parameters, so forwarding one there would answer
	// about every version while looking like a filtered answer.
	if p.Version != "" {
		if p.Operation != "get" {
			return fmt.Errorf("version is only valid for 'get' operation, not %q", p.Operation)
		}
		if !agento11yEffectiveVersionPattern.MatchString(p.Version) {
			return fmt.Errorf("version %q is invalid: it must be an effective version of the form 'sha256:<64 lowercase hex>' taken from 'list' or 'list_versions'; a declared version such as '1.4.2' is not accepted", p.Version)
		}
	}

	switch p.Operation {
	case "list":
		// Relative bounds re-resolve on every call, which changes the filter
		// hash the cursor is bound to and makes the API answer "cursor no
		// longer matches current filters".
		if p.Cursor != "" {
			for _, bound := range []struct{ name, value string }{{"start_time", p.StartTime}, {"end_time", p.EndTime}} {
				if agento11yRelativeTimeBound(bound.value) {
					return fmt.Errorf("paginating with a cursor requires repeating the same name_prefix, start_time, and end_time from the first page as absolute RFC3339 times: %s=%q is relative and drifts between calls, which invalidates the cursor", bound.name, bound.value)
				}
			}
		}
	case "get", "list_versions":
		if p.AgentName == nil {
			return fmt.Errorf("agent_name is required for %q operation (pass an explicitly empty string to select the unnamed agent)", p.Operation)
		}
	case "list_version_scores":
		if p.AgentName == nil {
			return fmt.Errorf("agent_name is required for 'list_version_scores' operation")
		}
		if strings.TrimSpace(*p.AgentName) == "" {
			return fmt.Errorf("agent_name must not be blank for 'list_version_scores' operation: the API has no score aggregates for the unnamed agent")
		}
	}

	// The time bounds are not parsed here: manageAgento11yAgents resolves them
	// through timeRange() before it issues a request, so a malformed bound is
	// reported from there.
	return nil
}

// timeRange resolves start_time and end_time. A zero time means the bound was
// not supplied and must not be sent.
func (p ManageAgento11yAgentsParams) timeRange() (time.Time, time.Time, error) {
	start, err := parseStartTime(p.StartTime)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing start_time: %w", err)
	}
	end, err := parseEndTime(p.EndTime)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing end_time: %w", err)
	}
	return start, end, nil
}

// agentName returns the resolved agent name. validate() guarantees the pointer
// is set for every operation that reads it.
func (p ManageAgento11yAgentsParams) agentName() string {
	if p.AgentName == nil {
		return ""
	}
	return *p.AgentName
}

func manageAgento11yAgents(ctx context.Context, args ManageAgento11yAgentsParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_agents: %w", err)
	}

	start, end, err := args.timeRange()
	if err != nil {
		return nil, fmt.Errorf("agento11y_manage_agents: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	switch args.Operation {
	case "list":
		return client.listAgento11yAgents(ctx, args.NamePrefix, start, end, args.Limit, args.Cursor)
	case "get":
		return client.getAgento11yAgent(ctx, args.agentName(), args.Version)
	case "list_versions":
		return client.listAgento11yAgentVersions(ctx, args.agentName(), args.Limit, args.Cursor)
	case "list_version_scores":
		return client.listAgento11yAgentVersionScores(ctx, args.agentName(), start, end)
	default:
		return nil, fmt.Errorf("agento11y_manage_agents: unknown operation %q", args.Operation)
	}
}

var ManageAgento11yAgents = mcpgrafana.MustTool(
	"agento11y_manage_agents",
	`Read the agent catalog of Grafana Agent Observability (the grafana-agento11y-app plugin): which agents send telemetry, what their system prompts and tools are, how their prompt versions evolved, and how each version scored.

The catalog is derived from ingested telemetry, not from a registration step: an agent exists here once its generations have been seen. It answers "what is this agent" while agento11y_manage_conversations and agento11y_manage_generations answer "what did it do".

Operations:
- 'list': agents in this tenant, newest activity first. Each row carries the latest effective version, first and latest seen times, generation and version counts, tool count, a system prompt prefix, and token_estimate. Paginated via limit and cursor
- 'get': one agent version in full: the complete system prompt, every tool with its JSON schema, and the models it ran on. Returns the latest version unless 'version' is set
- 'list_versions': the version history of one agent, one row per effective version with its seen window, generation count, tool count, and token_estimate. Paginated
- 'list_version_scores': evaluation score aggregates per version, with per-evaluator score_key, pass and fail counts, and mean_score. Use it to compare how versions scored

Versions: an effective version is always 'sha256:<64 lowercase hex>', and only that form is accepted by 'get'; a declared version such as '1.4.2' is reported in the declared_version fields but cannot be looked up. The plugin derives the effective version from the first of these the telemetry carries: the version the SDK reported, a hash of the declared version, a hash of the system prompt. Adding, removing, or editing a tool never mints a new version. Editing the prompt mints one only for an agent that reports neither its own effective version nor a declared version, so an agent that declares '1.4.2' keeps one effective version across prompt edits.

Response size: 'get' returns the whole system prompt plus every tool schema, which can be tens of thousands of tokens. Check token_estimate.total from 'list' or 'list_versions' before fetching, and prefer the system_prompt_prefix in those rows when a prefix is enough.

Cross-referencing: pass an agent name from 'list' to agento11y_manage_conversations as the search filter agent = "<name>" to find what that agent actually did.

Pagination: when a response carries next_cursor, call the same operation again with cursor set to it. For 'list', also repeat the same name_prefix, start_time, and end_time using absolute RFC3339 times; the cursor is bound to those filters and a relative value such as now-7d or 7d re-resolves and is rejected.

Permissions: every operation is a read and needs grafana-agento11y-app.data:read (Agento11y Editor or Admin). This tool performs no writes.

When to use:
- Discovering which agent names exist before filtering conversations or generations by agent
- Reading the system prompt or tool inventory an agent ran with, or how they changed between versions
- Checking whether a new prompt version scores worse than the previous one

When NOT to use:
- Reading individual conversations, generations, or their scores (use agento11y_manage_conversations and agento11y_manage_generations)
- Inspecting evaluators or the rules that schedule them (use agento11y_manage_evaluators and agento11y_manage_eval_rules)`,
	manageAgento11yAgents,
	mcp.WithTitleAnnotation("Manage Agent Observability agents"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)
