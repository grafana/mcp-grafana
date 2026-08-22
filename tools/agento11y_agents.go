package tools

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/gtime"
)

// Agent catalog of Agent Observability (/query/agents on the
// grafana-agento11y-app plugin resources proxy). The catalog is derived from
// ingested telemetry: an agent appears once its generations have been seen, and
// its generations are grouped by effective version, which the plugin takes from
// the version the SDK reported, else a hash of the declared agent version, else
// a hash of the system prompt.
//
// The agento11y_read tool's params, validation, and dispatch for this catalog
// live in agento11y_read_types.go and agento11y_read_handlers.go, alongside the
// conversations and generations operations it is merged with.

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
