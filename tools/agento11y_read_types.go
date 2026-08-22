package tools

import (
	"fmt"
	"strings"
	"time"
)

// Agento11yReadParams is the param struct for agento11y_read, which merges the
// three read-only agento11y_manage_* tools that carried no write half:
// agento11y_manage_agents, agento11y_manage_conversations, and
// agento11y_manage_generations. All three were already readOnlyHint: true, so
// this is a pure read tool with no agento11y_write counterpart, following the
// admin_read / alerting_manage_routing precedent for a read-only-only domain.
//
// The three sub-domains' operation names collided before merging (agents and
// conversations each had a bare "list" and a bare "get"; generations had its
// own "get"), so every operation here is namespaced by the resource it reads:
// list_agents/get_agent/list_agent_versions/list_agent_version_scores,
// list_conversations/search_conversations/get_conversation,
// get_generation/list_generation_scores.
type Agento11yReadParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=list_agents,enum=get_agent,enum=list_agent_versions,enum=list_agent_version_scores,enum=list_conversations,enum=search_conversations,enum=get_conversation,enum=get_generation,enum=list_generation_scores,description=The operation to perform. Agents: 'list_agents' for the agent catalog\\, 'get_agent' for one agent version in full (system prompt\\, tools\\, models)\\, 'list_agent_versions' for the version history of one agent\\, 'list_agent_version_scores' for evaluation score aggregates per version. Conversations: 'list_conversations' for recent conversations\\, 'search_conversations' to filter conversations by expression and time range\\, 'get_conversation' for one conversation with all its generations. Generations: 'get_generation' for one generation in full\\, 'list_generation_scores' for the evaluation scores of one generation."`

	// Agents: get_agent, list_agent_versions, list_agent_version_scores.
	// Pointer, so an omitted agent_name stays distinct from an explicit "",
	// which addresses the unnamed agent.
	AgentName *string `json:"agent_name,omitempty" jsonschema:"description=Agent name from 'list_agents'. Required for 'get_agent'\\, 'list_agent_versions'\\, and 'list_agent_version_scores'. An explicitly empty string selects the unnamed agent (telemetry with no agent name)\\, which 'get_agent' and 'list_agent_versions' accept and 'list_agent_version_scores' rejects."`
	// Agents: get_agent only.
	Version string `json:"version,omitempty" jsonschema:"description=Effective version 'sha256:<64 lowercase hex>' from 'list_agents' or 'list_agent_versions'\\, accepted only by 'get_agent'. Omit it for the latest version. Declared versions such as '1.4.2' are not accepted here; they are reported in the declared_version fields."`
	// Agents: list_agents only.
	NamePrefix string `json:"name_prefix,omitempty" jsonschema:"description=Agent name filter (for 'list_agents'). Despite the parameter name\\, the API matches it case-insensitively anywhere in the name\\, so 'agent' also returns 'my-agent'."`

	// Conversations: get_conversation.
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"description=The conversation ID (required for 'get_conversation' operation)"`
	// Conversations: search_conversations.
	Filters string `json:"filters,omitempty" jsonschema:"description=Filter expression (for 'search_conversations' operation). Format: key operator value with the value in double quotes\\, multiple filters separated by spaces. See the tool description for keys and operators."`

	// Generations: get_generation, list_generation_scores.
	GenerationID string `json:"generation_id,omitempty" jsonschema:"description=The generation ID (required for 'get_generation' and 'list_generation_scores' operations)"`

	// Shared pagination and time-window parameters. Semantics are documented
	// per operation rather than split into per-domain fields: agents' window
	// filters by last-seen time, conversations' by generation time, and both
	// resolve through the same relative/absolute time parser.
	StartTime string `json:"start_time,omitempty" jsonschema:"description=Start of the time range in RFC3339 or relative format (e.g. now-7d). For 'list_agents' it keeps agents last seen at or after this time and defaults to no lower bound. For 'list_agent_version_scores' it starts the score window; omitting it does not mean no lower bound\\, because the API then either scopes to the agent's 50 most recent versions or falls back to a 90-day window. For 'search_conversations' it starts the search window and defaults to now-24h."`
	EndTime   string `json:"end_time,omitempty" jsonschema:"description=End of the time range in RFC3339 or relative format (e.g. now). For 'list_agents' it keeps agents last seen at or before this time and defaults to no upper bound. For 'list_agent_version_scores' it ends the score window; passing it without start_time gives a 90-day window ending here. For 'search_conversations' it ends the search window and defaults to now."`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50). For 'list_agents' and 'list_agent_versions' it is capped at 200 by the API. (for 'list_agents'\\, 'list_agent_versions'\\, 'list_conversations'\\, 'search_conversations'\\, and 'list_generation_scores')"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor\\, echoed back exactly (for 'list_agents'\\, 'list_agent_versions'\\, 'list_conversations'\\, 'search_conversations'\\, and 'list_generation_scores'). For 'list_agents'\\, also resend the same name_prefix\\, start_time\\, and end_time as the first call using absolute RFC3339 times; the cursor is bound to those filters and a relative value such as now-7d or 7d drifts between calls and is rejected. For 'search_conversations'\\, also resend the same filters\\, start_time\\, and end_time from the first call using absolute RFC3339 times for the same reason."`
}

const agento11yReadOperations = "list_agents, get_agent, list_agent_versions, list_agent_version_scores, list_conversations, search_conversations, get_conversation, get_generation, list_generation_scores"

func (p Agento11yReadParams) validate() error {
	switch p.Operation {
	case "list_agents", "get_agent", "list_agent_versions", "list_agent_version_scores",
		"list_conversations", "search_conversations", "get_conversation",
		"get_generation", "list_generation_scores":
	default:
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yReadOperations)
	}

	// Only get_agent's lookup route reads a version. The other agent
	// operations ignore unknown query parameters, so forwarding one there
	// would answer about every version while looking like a filtered answer.
	if p.Version != "" {
		if p.Operation != "get_agent" {
			return fmt.Errorf("version is only valid for 'get_agent' operation, not %q", p.Operation)
		}
		if !agento11yEffectiveVersionPattern.MatchString(p.Version) {
			return fmt.Errorf("version %q is invalid: it must be an effective version of the form 'sha256:<64 lowercase hex>' taken from 'list_agents' or 'list_agent_versions'; a declared version such as '1.4.2' is not accepted", p.Version)
		}
	}

	switch p.Operation {
	case "list_agents":
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
	case "get_agent", "list_agent_versions":
		if p.AgentName == nil {
			return fmt.Errorf("agent_name is required for %q operation (pass an explicitly empty string to select the unnamed agent)", p.Operation)
		}
	case "list_agent_version_scores":
		if p.AgentName == nil {
			return fmt.Errorf("agent_name is required for 'list_agent_version_scores' operation")
		}
		if strings.TrimSpace(*p.AgentName) == "" {
			return fmt.Errorf("agent_name must not be blank for 'list_agent_version_scores' operation: the API has no score aggregates for the unnamed agent")
		}
	case "get_conversation":
		if p.ConversationID == "" {
			return fmt.Errorf("conversation_id is required for 'get_conversation' operation")
		}
	case "search_conversations":
		if _, err := p.searchConversationsRequest(); err != nil {
			return err
		}
	case "get_generation", "list_generation_scores":
		if p.GenerationID == "" {
			return fmt.Errorf("generation_id is required for %q operation", p.Operation)
		}
	}

	// The agent time bounds are not parsed here: agento11yRead resolves them
	// through agentTimeRange() before it issues a request, so a malformed
	// bound is reported from there.
	return nil
}

// agentTimeRange resolves start_time and end_time for the agent operations. A
// zero time means the bound was not supplied and must not be sent.
func (p Agento11yReadParams) agentTimeRange() (time.Time, time.Time, error) {
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
func (p Agento11yReadParams) agentName() string {
	if p.AgentName == nil {
		return ""
	}
	return *p.AgentName
}

// searchConversationsRequest builds the search request body. For the first
// page the time range defaults to the last 24 hours client-side (the plugin
// requires both bounds). When paginating, the backend binds the cursor to the
// exact filters and time window of the first page, so re-sending a different
// window (such as a re-resolved "now-24h" whose "now" has advanced) fails with
// "cursor no longer matches current filters". Rather than let drifting
// defaults trigger that confusing backend error, defaults are only applied
// without a cursor; a cursor requires explicit bounds and fails client-side
// otherwise.
func (p Agento11yReadParams) searchConversationsRequest() (Agento11ySearchRequest, error) {
	startStr, endStr := p.StartTime, p.EndTime
	if p.Cursor == "" {
		if startStr == "" {
			startStr = "now-24h"
		}
		if endStr == "" {
			endStr = "now"
		}
	} else if startStr == "" || endStr == "" {
		return Agento11ySearchRequest{}, fmt.Errorf("paginating with a cursor requires repeating the same start_time, end_time, and filters from the first page (use absolute RFC3339 times; relative ranges like now-24h drift between calls and invalidate the cursor)")
	}

	start, err := parseStartTime(startStr)
	if err != nil {
		return Agento11ySearchRequest{}, fmt.Errorf("parsing start_time: %w", err)
	}
	end, err := parseEndTime(endStr)
	if err != nil {
		return Agento11ySearchRequest{}, fmt.Errorf("parsing end_time: %w", err)
	}

	pageSize := p.Limit
	if pageSize <= 0 {
		pageSize = defaultAgento11yPageSize
	}

	return Agento11ySearchRequest{
		Filters:   p.Filters,
		TimeRange: &Agento11ySearchTimeRange{From: start, To: end},
		PageSize:  pageSize,
		Cursor:    p.Cursor,
	}, nil
}
