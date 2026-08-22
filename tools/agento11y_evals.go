package tools

import (
	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const agento11yEvalsReadDescription = `Read the evaluation control plane of Grafana Agent Observability (the grafana-agento11y-app plugin): evaluators, eval rules and guards, saved conversations and collections, offline experiments, and test suites. See agento11y_evals_write for the write-capable counterpart, and agento11y_read for the agent catalog, conversations, and generations these evaluate.

Evaluators (a scoring function - kind: llm_judge, json_schema, regex, or heuristic - that scores generations; agento11y_read's generation scores name the evaluator that produced them):
- 'list_evaluators': evaluators in this tenant (paginated)
- 'get_evaluator': one evaluator by ID, with its kind, config, and output_keys
- 'list_templates': evaluator templates, filterable by scope ('global' for built-ins, 'tenant' for locally created ones)
- 'get_template': one template with its config, output_keys, and version list
- 'list_template_versions': version history of a template
- 'list_judge_providers': judge providers configured on this stack
- 'list_judge_models': judge models, optionally filtered by provider

Eval rules and guards - two different resources with different runtime behavior. Rules (/eval/rules) are asynchronous: a rule selects production traffic (selector, match filters, sample_rate) and schedules its evaluator_ids to score matching generations after the fact; rules only observe. Guards (/eval/hook-rules) run inline on the request path and can deny it, redact content, or block tool calls; a guard is inert until the agent application calls the hooks endpoint itself.
- 'list_rules': asynchronous eval rules in this tenant (paginated)
- 'get_rule': one eval rule by ID
- 'list_guards': guards (paginated)
- 'get_guard': one guard by ID

Saved conversations and collections - two linked resources. A saved conversation (/eval/saved-conversations) is a bookmark on one conversation, keyed by a saved_id, giving it a stable ID, a name, and tags a collection can reference. A collection (/eval/collections) is a named group of saved conversations, used as source material for offline evaluation; it holds saved conversations, never raw conversation IDs.
- 'list_saved_conversations': bookmarked conversations, filterable by source ('telemetry' or 'manual'). Also reports total_count for the whole filtered set
- 'get_saved_conversation': one bookmark by ID
- 'list_collections_for_saved_conversation': the collections one bookmark belongs to (unpaginated)
- 'list_collections': collections in this tenant, each with its member_count
- 'get_collection': one collection by ID
- 'list_collection_members': the saved conversations in a collection. List rows are already enriched with generation_count, total_tokens, agent_names, models, and tags - prefer that over calling 'list_collections_for_saved_conversation' per row

Experiments - one offline run of an agent over a test suite. Each test case produces one or more trials, each trial is scored by the experiment's evaluators, and the experiment reports a pass rate. Experiments are created by SDK runners, not from here.
- 'list_experiments': experiments in this tenant, filterable by suite_id, status, experiment_source, created_by, tag, and a created_at or completed_at window. Each row carries the same result summary as 'get_experiment'
- 'get_experiment': one experiment with its result summary: pass rate, average final score, total cost and tokens
- 'get_experiment_report': the per-test-case breakdown, trimmed by row_limit. Test case input/expected, score records, and artifact records are dropped in favor of score_count/artifact_count and the IDs the drill-downs take. The report is fetched whole before it is trimmed, and a response above 10 MiB fails the call rather than arriving truncated
- 'list_experiment_trials': one experiment's trials, paginated. Prefer this over 'get_experiment_report' on a large suite
- 'list_experiment_scores': every score in one experiment, paginated
- 'get_trial': one trial in full, including the test case snapshot
- 'list_trial_scores': one trial's scores, with the explanation each judge wrote
- 'list_trial_artifacts': one trial's artifact metadata, with a content_ref rather than the bytes
- 'list_experiment_facets': the distinct suites, owners, and tags across every experiment, for building a 'list_experiments' filter. Only experiment_source, from, and to narrow it

Test suites - the input side of an offline experiment: a named, versioned set of test cases that an SDK runner replays against an agent. A version is either a draft or published; a draft accepts test case edits, and publishing freezes it and makes it the suite's latest_version. A suite has at most one draft at a time.
- 'list_suites': test suites in this tenant, newest first
- 'get_suite': one suite with its full version history
- 'list_test_cases': the test cases of one suite version, paginated
- 'get_test_case': one test case in full, with its free-form input and expected values

Identifiers: evaluator_id and rule_id accept only letters, digits, '_', and '.'; saved_id is looser (also '-' and ':'); collection_id and suite version identifiers are server-assigned or caller-chosen per operation, as described above.

Pagination: when a response carries next_cursor, call the same operation again with cursor set to it, repeating the first page's filters using absolute RFC3339 times - a relative bound such as now-7d re-resolves between calls and invalidates the cursor.

Permissions: every operation here needs grafana-agento11y-app.data:read (Agento11y Editor or Admin). This tool performs no writes; see agento11y_evals_write for creating, updating, deleting, previewing, or testing.

When to use:
- A score from agento11y_read names an evaluator, rule, or experiment and you need to see what it checks or how it scored
- Auditing which guards are live and whether they warn or deny
- Reading what is already curated (collections, test suites) before running or extending an experiment
- Finding the last experiment for a suite after a suspected regression, then drilling into a failing trial

When NOT to use:
- Creating, updating, deleting, previewing a rule, or test-scoring an evaluator (use agento11y_evals_write)
- Reading agents, conversations, or generations themselves (use agento11y_read)`

const agento11yEvalsWriteDescription = `Manage the evaluation control plane of Grafana Agent Observability (the grafana-agento11y-app plugin): create, update, delete, preview, or test evaluators, eval rules and guards, saved conversations and collections, experiments, and test suites. See agento11y_evals_read for the read operations of every sub-domain (including 'preview_rule' and 'test_evaluator', which live here despite persisting nothing, because they need eval:write).

Evaluators:
- 'upsert_evaluator': create or update an evaluator from an inline 'definition'. POST is create-or-update keyed on definition.evaluator_id; there is no separate update, and re-using an existing version returns 409, so bump the version to change an evaluator
- 'delete_evaluator': soft-delete an evaluator by ID. Rules and guards that reference it keep the reference and silently stop producing scores, so check agento11y_evals_read's 'list_rules'/'list_guards' first
- 'fork_template': derive a new evaluator from a template in one call. Prefer this over copying 'get_template' output into 'upsert_evaluator', which the API rejects
- 'test_evaluator': run an inline evaluator definition against one generation and return its scores without persisting anything. Useful for tuning a judge config before 'upsert_evaluator'

Eval rules and guards (rules are asynchronous and only observe; guards run inline and can deny, redact, or block):
- 'create_rule': create an asynchronous eval rule from an inline 'definition'
- 'update_rule': patch an existing rule; send only the fields to change (rule_id comes from the 'rule_id' parameter and must not appear in the definition)
- 'delete_rule': delete a rule by ID
- 'preview_rule': dry-run a selector, match, and sample_rate against recent traffic and return how many generations would match and be sampled, plus example generations. Run this before creating a rule that spends judge tokens
- 'create_guard': create an inline guard (stored as a hook rule)
- 'update_guard': full replace of a guard (PUT, not PATCH) - omitted fields reset to server defaults, so send the complete definition, normally a 'get_guard' result with your edits applied
- 'delete_guard': delete a guard by ID

Saved conversations and collections:
- 'save_conversation': bookmark a live conversation by conversation_id. saved_id is optional and defaults to 'saved-<conversation_id>'; a conversation can only be saved once, so a repeat returns 409 naming the existing saved_id
- 'delete_saved_conversation': delete a bookmark by saved_id. Idempotent, and it also removes the bookmark from every collection it belonged to
- 'create_collection': create an empty collection from a name and optional description. The response carries the server-assigned collection_id needed by the membership operations
- 'update_collection': patch a collection's name or description. Omitted fields are left unchanged, and an explicitly empty description clears it
- 'delete_collection': delete a collection and its memberships in one transaction. Idempotent; the saved conversations themselves are kept
- 'add_collection_members': add saved_ids to a collection. Every ID must already be a saved conversation; re-adding an existing member is a no-op
- 'remove_collection_member': drop one saved conversation from a collection. Idempotent; the bookmark itself is kept

Experiments:
- 'update_experiment': patch an experiment's name, description, tags, or metadata. Only the experiment's created_by may patch it; a finished experiment rejects a name change with 409
- 'cancel_experiment': stop a running experiment. Any caller with the write permission can stop any experiment. An already-finished experiment is left alone and returned unchanged rather than failing, so read the status on the result

Test suites:
- 'create_suite': a new empty suite. It has no version yet, so follow it with 'create_draft_version'
- 'update_suite': patch a suite's name, description, or tags
- 'create_draft_version': open a new editable version. A suite that already has a draft answers 409
- 'publish_version': freeze a draft. There is no unpublish; a published version answers 409 to a second publish and to every test case edit, so changing a published suite means a new draft
- 'upsert_test_case': write a whole test case into a draft version. It replaces the stored case rather than merging into it, so a field left out is cleared; read the case with agento11y_evals_read's 'get_test_case' first and send it back complete
- 'delete_test_case': remove one test case from a draft version. Deleting one that is already gone answers 404

Permissions: every write here, plus 'preview_rule' and 'test_evaluator' (which persist nothing), needs grafana-agento11y-app.eval:write, granted only by the Agento11y Admin role; an Editor token gets 403.

When to use:
- Creating an evaluator, then binding it to production traffic with 'create_rule' after checking the blast radius with 'preview_rule'
- Adding a guard, or promoting one from warn to deny after watching its false-positive rate
- Turning a triaged failure into a regression collection: 'save_conversation', then 'create_collection' or 'add_collection_members'
- Labelling or stopping an experiment, or adding a regression case to a suite and publishing the draft

When NOT to use:
- Reading any of the above without changing it (use agento11y_evals_read)
- Reading or filtering agents, conversations, or generations (use agento11y_read)`

var Agento11yEvalsRead = mcpgrafana.MustTool(
	"agento11y_evals_read",
	agento11yEvalsReadDescription,
	agento11yEvalsRead,
	mcp.WithTitleAnnotation("Read Agent Observability evaluators, rules, collections, experiments, and test suites"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var Agento11yEvalsWrite = mcpgrafana.MustTool(
	"agento11y_evals_write",
	agento11yEvalsWriteDescription,
	agento11yEvalsWrite,
	mcp.WithTitleAnnotation("Manage Agent Observability evaluators, rules, collections, experiments, and test suites"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)
