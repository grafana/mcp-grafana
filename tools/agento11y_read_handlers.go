package tools

import (
	"context"
	"fmt"
	"net/url"
)

// agento11yRead dispatches agento11y_read's nine operations across the three
// merged sub-domains (agent catalog, conversations, generations). None of them
// write, so there is a single dispatch with no read/write split.
func agento11yRead(ctx context.Context, args Agento11yReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_read: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	switch args.Operation {
	case "list_agents":
		start, end, err := args.agentTimeRange()
		if err != nil {
			return nil, fmt.Errorf("agento11y_read: %w", err)
		}
		return client.listAgento11yAgents(ctx, args.NamePrefix, start, end, args.Limit, args.Cursor)
	case "get_agent":
		return client.getAgento11yAgent(ctx, args.agentName(), args.Version)
	case "list_agent_versions":
		return client.listAgento11yAgentVersions(ctx, args.agentName(), args.Limit, args.Cursor)
	case "list_agent_version_scores":
		start, end, err := args.agentTimeRange()
		if err != nil {
			return nil, fmt.Errorf("agento11y_read: %w", err)
		}
		return client.listAgento11yAgentVersionScores(ctx, args.agentName(), start, end)
	case "list_conversations":
		return client.listAgento11yConversations(ctx, args.Limit, args.Cursor)
	case "search_conversations":
		req, err := args.searchConversationsRequest()
		if err != nil {
			return nil, fmt.Errorf("agento11y_read: %w", err)
		}
		return client.searchAgento11yConversations(ctx, req)
	case "get_conversation":
		return client.getAgento11yDetail(ctx, "/query/conversations/"+url.PathEscape(args.ConversationID), "conversation")
	case "get_generation":
		return client.getAgento11yDetail(ctx, "/query/generations/"+url.PathEscape(args.GenerationID), "generation")
	case "list_generation_scores":
		return client.listAgento11yGenerationScores(ctx, args.GenerationID, args.Limit, args.Cursor)
	default:
		// Unreachable once validate() has passed; kept for defense in depth.
		return nil, fmt.Errorf("agento11y_read: unknown operation %q", args.Operation)
	}
}
