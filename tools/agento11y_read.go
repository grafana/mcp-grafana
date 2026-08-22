package tools

import (
	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const agento11yReadDescription = `Read the agent catalog, conversations, and generations of Grafana Agent Observability (the grafana-agento11y-app plugin): which agents send telemetry, what they said, and how it scored.

Merges the three read-only agento11y tools that carried no write half: what was agento11y_manage_agents, agento11y_manage_conversations, and agento11y_manage_generations. All operations below are read-only; see agento11y_evals_read and agento11y_evals_write for evaluators, eval rules and guards, saved conversations and collections, experiments, and test suites.

Agent catalog operations (answer "what is this agent"; derived from ingested telemetry, not a registration step):
- 'list_agents': agents in this tenant, newest activity first. Each row carries the latest effective version, first and latest seen times, generation and version counts, tool count, a system prompt prefix, and token_estimate. Paginated
- 'get_agent': one agent version in full: the complete system prompt, every tool with its JSON schema, and the models it ran on. Returns the latest version unless 'version' is set
- 'list_agent_versions': the version history of one agent, one row per effective version with its seen window, generation count, tool count, and token_estimate. Paginated
- 'list_agent_version_scores': evaluation score aggregates per version, with per-evaluator score_key, pass and fail counts, and mean_score. Use it to compare how versions scored

Versions: an effective version is always 'sha256:<64 lowercase hex>', and only that form is accepted by 'get_agent'; a declared version such as '1.4.2' is reported in the declared_version fields but cannot be looked up. The plugin derives the effective version from the first of these the telemetry carries: the version the SDK reported, a hash of the declared version, a hash of the system prompt. Adding, removing, or editing a tool never mints a new version. Editing the prompt mints one only for an agent that reports neither its own effective version nor a declared version, so an agent that declares '1.4.2' keeps one effective version across prompt edits.

Agent response size: 'get_agent' returns the whole system prompt plus every tool schema, which can be tens of thousands of tokens. Check token_estimate.total from 'list_agents' or 'list_agent_versions' before fetching, and prefer the system_prompt_prefix in those rows when a prefix is enough.

Conversation operations (answer "what did this agent do"):
- 'list_conversations': recent conversations (lightweight; id, title, generation count, timestamps), paginated
- 'search_conversations': search conversations by filter expression and time range; results include models, agents, error counts, rating and eval summaries, and trace IDs. Pass an agent name from 'list_agents' as agent = "<name>" to scope the search to it
- 'get_conversation': one conversation by ID with all its generations, including full prompts and outputs (can be large)

Filter syntax for 'search_conversations': key operator value, with the value in double quotes; multiple filters are separated by spaces and combined with AND.
Filter keys (trace): model, provider, agent, agent.version, status, error.type, error.category, duration, tool.name, operation, namespace, cluster, service
Filter keys (metadata): generation_count, eval.passed, eval.evaluator_id, eval.score_key, eval.score
Operators: =, !=, >, <, >=, <=, =~ (regex)
Example: status = "error" agent = "claude-code"

Generation operations (drilling into one turn found via 'search_conversations' or 'get_conversation'):
- 'get_generation': full generation detail by ID, including prompt, output, model, and usage (can be large)
- 'list_generation_scores': evaluation scores for a generation (evaluator, score key, score type, value, passed, explanation). The evaluator named in a score can be inspected with agento11y_evals_read

Pagination: when a response has next_cursor, fetch the next page by calling the same operation again with cursor set to next_cursor. For 'list_agents' and 'search_conversations', also resend the same filters from the first call using absolute RFC3339 times; relative ranges like now-24h or now-7d shift between calls and the cursor will be rejected.

Permissions: every operation is a read and needs grafana-agento11y-app.data:read (Agento11y Editor or Admin). This tool performs no writes.

When to use:
- Discovering which agent names exist before filtering conversations by agent, or reading the system prompt or tool inventory an agent ran with
- Debugging an AI application: find failing or low-rated conversations, then inspect their generations and scores
- Reviewing evaluation results and user ratings across conversations, or checking whether a new agent version scores worse than the previous one

When NOT to use:
- Inspecting or changing evaluators, eval rules, guards, saved conversations, collections, experiments, or test suites (use agento11y_evals_read and agento11y_evals_write)`

var Agento11yRead = mcpgrafana.MustTool(
	"agento11y_read",
	agento11yReadDescription,
	agento11yRead,
	mcp.WithTitleAnnotation("Read Agent Observability agents, conversations, and generations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)
