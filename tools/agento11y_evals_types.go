package tools

import (
	"errors"
	"fmt"
	"strings"
)

// Agento11yEvalsReadParams and Agento11yEvalsWriteParams are the param structs
// for agento11y_evals_read and agento11y_evals_write, which merge the five
// write-capable agento11y_manage_* tools (evaluators, eval rules/guards,
// saved conversations/collections, experiments, test suites) into a
// disjoint-operation read/write pair.
//
// Each sub-domain kept its own operation names except experiments, whose
// original names (list, get, get_report, list_trials, list_scores,
// list_facets, update, cancel) were too generic once merged with the other
// four domains' operations into one 30-operation read enum and one
// 26-operation write enum; they are namespaced here as list_experiments,
// get_experiment, get_experiment_report, list_experiment_trials,
// list_experiment_scores, list_experiment_facets, update_experiment, and
// cancel_experiment. No other operation name collided across the five
// domains, so the rest are unchanged from their pre-consolidation names.
//
// Fields are declared flat (not via the sub-domains' agento11yXFields structs)
// because several field names collide across domains with incompatible
// types or semantics once embedded into one struct (Limit/Cursor appear in
// every sub-domain; Tags is a map[string]string for saved conversations but
// a *[]string for experiments and test suites): embedding same-named fields
// at the same depth makes them ambiguous to both the Go compiler and JSON
// (de)serialization. Where two sub-domains' fields are compatible in type and
// "replace the whole value" semantics, one field is shared (Name, Description,
// Tags, Definition, Metadata); where they are not, they get distinct names
// (Source for saved-conversation filtering vs ExperimentSource for experiment
// filtering; ConversationTags for a bookmark's tags vs the shared Tags for
// experiment/suite tag lists).

const agento11yEvalsReadOperations = agento11yEvaluatorReadOperations + ", " +
	agento11yEvalRuleReadOperations + ", " +
	agento11yEvalCollectionReadOperations + ", " +
	agento11yExperimentReadOperations + ", " +
	agento11yTestSuiteReadOperations

const agento11yEvalsWriteOperations = "upsert_evaluator, delete_evaluator, fork_template, test_evaluator, " +
	"create_rule, update_rule, delete_rule, preview_rule, create_guard, update_guard, delete_guard, " +
	"save_conversation, delete_saved_conversation, create_collection, update_collection, delete_collection, add_collection_members, remove_collection_member, " +
	"update_experiment, cancel_experiment, " +
	"create_suite, update_suite, create_draft_version, publish_version, upsert_test_case, delete_test_case"

// Agento11yEvalsReadParams is the param struct for agento11y_evals_read.
type Agento11yEvalsReadParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=list_evaluators,enum=get_evaluator,enum=list_templates,enum=get_template,enum=list_template_versions,enum=list_judge_providers,enum=list_judge_models,enum=list_rules,enum=get_rule,enum=list_guards,enum=get_guard,enum=list_saved_conversations,enum=get_saved_conversation,enum=list_collections_for_saved_conversation,enum=list_collections,enum=get_collection,enum=list_collection_members,enum=list_experiments,enum=get_experiment,enum=get_experiment_report,enum=list_experiment_trials,enum=list_experiment_scores,enum=get_trial,enum=list_trial_scores,enum=list_trial_artifacts,enum=list_experiment_facets,enum=list_suites,enum=get_suite,enum=list_test_cases,enum=get_test_case,description=The operation to perform. Evaluators: 'list_evaluators'\\, 'get_evaluator'\\, 'list_templates'\\, 'get_template'\\, 'list_template_versions'\\, 'list_judge_providers'\\, 'list_judge_models'. Eval rules/guards: 'list_rules'\\, 'get_rule'\\, 'list_guards'\\, 'get_guard'. Saved conversations/collections: 'list_saved_conversations'\\, 'get_saved_conversation'\\, 'list_collections_for_saved_conversation'\\, 'list_collections'\\, 'get_collection'\\, 'list_collection_members'. Experiments: 'list_experiments'\\, 'get_experiment'\\, 'get_experiment_report'\\, 'list_experiment_trials'\\, 'list_experiment_scores'\\, 'get_trial'\\, 'list_trial_scores'\\, 'list_trial_artifacts'\\, 'list_experiment_facets'. Test suites: 'list_suites'\\, 'get_suite'\\, 'list_test_cases'\\, 'get_test_case'. See agento11y_evals_write for the write-capable counterpart of each sub-domain except test suites' create_draft_version/publish_version\\, which have no read-side equivalent operation."`

	// Shared pagination, used by most list operations across every sub-domain.
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max varies by operation\\, e.g. 200 for 'list_agents'-style routes and 500 for the eval list routes). For most paginated list operations."`
	Cursor string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor\\, echoed back exactly and never constructed or incremented (for most paginated list operations). A cursor belongs to the operation and filters that produced it - it is not interchangeable across operations\\, and 'list_experiments'/'list_saved_conversations'-style operations reject a cursor paired with a relative time bound such as now-7d\\, since that would drift the window between calls."`

	// Evaluators: get_evaluator.
	EvaluatorID string `json:"evaluator_id,omitempty" jsonschema:"description=Evaluator ID (required for 'get_evaluator')"`
	// Evaluators: get_template, list_template_versions.
	TemplateID string `json:"template_id,omitempty" jsonschema:"description=Template ID (required for 'get_template' and 'list_template_versions')"`
	// Evaluators: list_templates.
	Scope string `json:"scope,omitempty" jsonschema:"enum=global,enum=tenant,description=Template scope filter: 'global' for built-in templates\\, 'tenant' for templates created in this tenant (for 'list_templates')"`
	// Evaluators: list_judge_models.
	Provider string `json:"provider,omitempty" jsonschema:"description=Judge provider ID to filter models by\\, e.g. 'openai' (for 'list_judge_models')"`

	// Eval rules/guards: get_rule, get_guard. RuleID addresses either a rule
	// or a guard depending on the operation.
	RuleID string `json:"rule_id,omitempty" jsonschema:"description=Eval rule ID (for 'get_rule') or guard ID (for 'get_guard'). Required for both."`

	// Saved conversations/collections.
	SavedID      string `json:"saved_id,omitempty" jsonschema:"description=Saved conversation ID (required for 'get_saved_conversation' and 'list_collections_for_saved_conversation')"`
	CollectionID string `json:"collection_id,omitempty" jsonschema:"description=Collection ID (required for 'get_collection' and 'list_collection_members')"`
	Source       string `json:"source,omitempty" jsonschema:"enum=telemetry,enum=manual,description=Saved conversation source filter: 'telemetry' for bookmarked production conversations\\, 'manual' for hand-built ones (for 'list_saved_conversations')"`

	// Experiments.
	ExperimentID     string   `json:"experiment_id,omitempty" jsonschema:"description=Experiment ID from 'list_experiments' (required for 'get_experiment'\\, 'get_experiment_report'\\, 'list_experiment_trials'\\, and 'list_experiment_scores')"`
	TrialID          string   `json:"trial_id,omitempty" jsonschema:"description=Trial ID from 'list_experiment_trials' or 'get_experiment_report' (required for 'get_trial'\\, 'list_trial_scores'\\, and 'list_trial_artifacts')"`
	Status           string   `json:"status,omitempty" jsonschema:"enum=running,enum=completed,enum=failed,enum=canceled,description=Experiment status filter (for 'list_experiments'). A finished run is 'completed'; there is no 'succeeded' status."`
	ExperimentSource string   `json:"experiment_source,omitempty" jsonschema:"enum=collection,enum=external,description=Experiment origin filter: 'collection' for runs built from a saved collection\\, 'external' for runs reported by an SDK runner (for 'list_experiments' and 'list_experiment_facets'). Distinct from the saved-conversation 'source' filter above."`
	CreatedBy        string   `json:"created_by,omitempty" jsonschema:"description=Owner filter\\, matched against the values 'list_experiment_facets' reports under owners (for 'list_experiments')"`
	Tag              []string `json:"tag,omitempty" jsonschema:"description=Experiment tag filter (for 'list_experiments'). Several tags match an experiment carrying any of them\\, not all of them."`
	From             string   `json:"from,omitempty" jsonschema:"description=Start of the created_at window in RFC3339 or relative format (e.g. now-7d)\\, for 'list_experiments' and 'list_experiment_facets'"`
	To               string   `json:"to,omitempty" jsonschema:"description=End of the created_at window in RFC3339 or relative format (e.g. now)\\, for 'list_experiments' and 'list_experiment_facets'"`
	CompletedFrom    string   `json:"completed_from,omitempty" jsonschema:"description=Start of the completed_at window in RFC3339 or relative format (for 'list_experiments'). 'from' and 'to' filter created_at; this pair filters completed_at instead\\, so it drops runs that have not finished."`
	CompletedTo      string   `json:"completed_to,omitempty" jsonschema:"description=End of the completed_at window in RFC3339 or relative format (for 'list_experiments')"`
	Order            string   `json:"order,omitempty" jsonschema:"enum=created_at_desc,enum=completed_at_desc,description=Sort order for 'list_experiments'\\, newest first either way. Defaults to 'created_at_desc'. 'completed_at_desc' returns a single page and cannot be combined with a cursor."`
	RowLimit         int      `json:"row_limit,omitempty" jsonschema:"description=Maximum number of test case rows 'get_experiment_report' returns (default 50\\, max 500). The report route is not paginated\\, so this trims the returned rows and sets rows_truncated; it does not reduce what the plugin sends."`

	// Test suites. SuiteID doubles as a list_experiments filter above and as
	// the suite identifier here.
	SuiteID    string `json:"suite_id,omitempty" jsonschema:"description=Test suite ID. For 'list_experiments' it filters by suite. For the test-suite operations it identifies the suite\\, taken from 'list_suites' (required for 'get_suite'\\, 'list_test_cases'\\, and 'get_test_case')."`
	Version    string `json:"version,omitempty" jsonschema:"description=Suite version such as 'v3'\\, taken from the versions list of 'get_suite' (required for 'list_test_cases' and 'get_test_case'). There is no operation for reading a version on its own."`
	TestCaseID string `json:"test_case_id,omitempty" jsonschema:"description=Test case ID from 'list_test_cases' (required for 'get_test_case')"`
}

func (p Agento11yEvalsReadParams) evaluatorReadRequest() agento11yEvaluatorReadRequest {
	return agento11yEvaluatorReadRequest{
		agento11yEvaluatorFields: agento11yEvaluatorFields{Scope: p.Scope, Provider: p.Provider, Limit: p.Limit, Cursor: p.Cursor},
		EvaluatorID:              p.EvaluatorID,
		TemplateID:               p.TemplateID,
	}
}

func (p Agento11yEvalsReadParams) evalRuleReadRequest() agento11yEvalRuleReadRequest {
	return agento11yEvalRuleReadRequest{
		agento11yEvalRuleFields: agento11yEvalRuleFields{Limit: p.Limit, Cursor: p.Cursor},
		RuleID:                  p.RuleID,
	}
}

func (p Agento11yEvalsReadParams) evalCollectionReadRequest() agento11yEvalCollectionReadRequest {
	return agento11yEvalCollectionReadRequest{
		agento11yEvalCollectionFields: agento11yEvalCollectionFields{Source: p.Source, Limit: p.Limit, Cursor: p.Cursor},
		SavedID:                       p.SavedID,
		CollectionID:                  p.CollectionID,
	}
}

func (p Agento11yEvalsReadParams) experimentReadRequest() agento11yExperimentReadRequest {
	return agento11yExperimentReadRequest{
		agento11yExperimentFields: agento11yExperimentFields{
			SuiteID:       p.SuiteID,
			Status:        p.Status,
			Source:        p.ExperimentSource,
			CreatedBy:     p.CreatedBy,
			Tag:           p.Tag,
			From:          p.From,
			To:            p.To,
			CompletedFrom: p.CompletedFrom,
			CompletedTo:   p.CompletedTo,
			Order:         p.Order,
			Limit:         p.Limit,
			Cursor:        p.Cursor,
			RowLimit:      p.RowLimit,
		},
		ExperimentID: p.ExperimentID,
		TrialID:      p.TrialID,
	}
}

func (p Agento11yEvalsReadParams) testSuiteReadRequest() agento11yTestSuiteReadRequest {
	return agento11yTestSuiteReadRequest{
		agento11yTestSuiteFields: agento11yTestSuiteFields{Limit: p.Limit, Cursor: p.Cursor},
		SuiteID:                  p.SuiteID,
		Version:                  p.Version,
		TestCaseID:               p.TestCaseID,
	}
}

// validate tries each of the five sub-domains' own read-operation validators
// in turn; each returns the errAgento11yUnknownOperation sentinel for an
// operation it does not own, so the first non-sentinel result (nil or a
// validation error) is the answer. An operation none of the five recognize
// falls through to the combined "unknown operation" message.
func (p Agento11yEvalsReadParams) validate() error {
	if err := p.evaluatorReadRequest().validateOperation(p.Operation); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.evalRuleReadRequest().validateOperation(p.Operation); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.evalCollectionReadRequest().validateOperation(p.Operation); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.experimentReadRequest().validateOperation(p.Operation); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.testSuiteReadRequest().validateOperation(p.Operation); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yEvalsReadOperations)
}

// Agento11yEvalsWriteParams is the param struct for agento11y_evals_write.
// Its operation enum is disjoint from Agento11yEvalsReadParams's: every value
// here is a write (or a persists-nothing dry run like 'preview_rule' and
// 'test_evaluator' that still needs eval:write). validate() never accepts a
// read operation - see its doc comment.
type Agento11yEvalsWriteParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=upsert_evaluator,enum=delete_evaluator,enum=fork_template,enum=test_evaluator,enum=create_rule,enum=update_rule,enum=delete_rule,enum=preview_rule,enum=create_guard,enum=update_guard,enum=delete_guard,enum=save_conversation,enum=delete_saved_conversation,enum=create_collection,enum=update_collection,enum=delete_collection,enum=add_collection_members,enum=remove_collection_member,enum=update_experiment,enum=cancel_experiment,enum=create_suite,enum=update_suite,enum=create_draft_version,enum=publish_version,enum=upsert_test_case,enum=delete_test_case,description=The operation to perform. Evaluators: 'upsert_evaluator' (create or update from 'definition')\\, 'delete_evaluator'\\, 'fork_template' (derive an evaluator from a template)\\, 'test_evaluator' (score one generation with an inline definition without storing it). Eval rules/guards: 'create_rule'\\, 'update_rule' (PATCH\\, partial)\\, 'delete_rule'\\, 'preview_rule' (dry-run\\, stores nothing)\\, 'create_guard'\\, 'update_guard' (PUT\\, full replace)\\, 'delete_guard'. Saved conversations/collections: 'save_conversation'\\, 'delete_saved_conversation'\\, 'create_collection'\\, 'update_collection' (PATCH\\, partial)\\, 'delete_collection'\\, 'add_collection_members'\\, 'remove_collection_member'. Experiments: 'update_experiment' (PATCH the name\\, description\\, tags\\, or metadata)\\, 'cancel_experiment'. Test suites: 'create_suite'\\, 'update_suite' (PATCH)\\, 'create_draft_version'\\, 'publish_version'\\, 'upsert_test_case'\\, 'delete_test_case'. See agento11y_evals_read for the read operations of every sub-domain except eval rules/guards' preview_rule and evaluators' test_evaluator\\, which are here because they need eval:write even though they persist nothing."`

	// Evaluators.
	EvaluatorID  string `json:"evaluator_id,omitempty" jsonschema:"description=Evaluator ID (required for 'delete_evaluator'). For 'upsert_evaluator' and 'fork_template' the evaluator ID belongs in 'definition' instead\\, because the API reads it from the request body."`
	TemplateID   string `json:"template_id,omitempty" jsonschema:"description=Template ID (required for 'fork_template')"`
	GenerationID string `json:"generation_id,omitempty" jsonschema:"description=Generation to score (required for 'test_evaluator'). Find one with agento11y_read."`

	// Shared free-form request body, used by the evaluator and eval
	// rule/guard write operations. The exact shape differs by operation; see
	// the operation description above and validate()'s per-operation checks.
	Definition map[string]any `json:"definition,omitempty" jsonschema:"description=Request body for 'upsert_evaluator'\\, 'fork_template'\\, 'test_evaluator'\\, 'create_rule'\\, 'update_rule'\\, 'preview_rule'\\, 'create_guard'\\, and 'update_guard'. For 'upsert_evaluator': evaluator_id (string\\, required\\, only letters/digits/underscore/dot)\\, version (string\\, required)\\, kind (llm_judge|json_schema|regex|heuristic)\\, description\\, config (object)\\, output_keys (array of {key\\, type\\, description\\, unit\\, pass_threshold\\, enum\\, min\\, max\\, pass_match\\, pass_value}). For 'fork_template': evaluator_id (required) plus optional version\\, config\\, and output_keys overrides. For 'test_evaluator': kind\\, config\\, and output_keys - the API rejects evaluator_id and version here. For 'create_rule'/'update_rule': rule_id (required on create)\\, enabled\\, selector (user_visible_turn|all_assistant_generations|tool_call_steps|errored_generations|conversation)\\, match (object of string arrays)\\, sample_rate (0-1)\\, evaluator_ids\\, alert_rule_uids\\, min_idle_seconds (required when selector is 'conversation'). 'update_rule' must not include rule_id in the body - it comes from the rule_id parameter. 'preview_rule': selector\\, match\\, sample_rate\\, and optionally rule_id. For 'create_guard'/'update_guard': rule_id (required on create)\\, enabled\\, phase (preflight|postflight)\\, priority\\, selector (as above\\, plus 'all')\\, match\\, action_on_fail (warn|deny)\\, short_circuit\\, and exactly ONE decision shape: evaluator_ids\\, redact ({patterns: [{id\\, regex}]})\\, or tool_filter ({blocked_names: [glob]}). 'update_guard' is a full replace: omitted fields reset to server defaults."`

	// Eval rules/guards. RuleID addresses either a rule or a guard depending
	// on the operation, matching the pre-consolidation tool's dual use.
	RuleID string `json:"rule_id,omitempty" jsonschema:"description=Eval rule ID (for 'update_rule' and 'delete_rule') or guard ID (for 'update_guard' and 'delete_guard'). For 'create_rule'\\, 'create_guard'\\, and 'preview_rule' the ID comes from 'definition' instead."`

	// Saved conversations/collections.
	SavedID          string            `json:"saved_id,omitempty" jsonschema:"description=Saved conversation ID. Required for 'delete_saved_conversation' and 'remove_collection_member'. Optional for 'save_conversation'\\, which derives 'saved-<conversation_id>' when it is omitted. Only letters\\, digits\\, '_'\\, '.'\\, ':'\\, and '-' are accepted."`
	CollectionID     string            `json:"collection_id,omitempty" jsonschema:"description=Collection ID. Required for 'update_collection'\\, 'delete_collection'\\, 'add_collection_members'\\, and 'remove_collection_member'. Rejected by 'create_collection' (the API assigns a UUID) and by 'save_conversation' (bookmarking cannot also add to a collection)."`
	ConversationID   string            `json:"conversation_id,omitempty" jsonschema:"description=Conversation to bookmark (required for 'save_conversation'). Find one with agento11y_read."`
	ConversationTags map[string]string `json:"conversation_tags,omitempty" jsonschema:"description=Optional string tags to store on a bookmark (for 'save_conversation'). Distinct from the 'tags' list below\\, which replaces an experiment's or test suite's tag list."`
	SavedIDs         []string          `json:"saved_ids,omitempty" jsonschema:"description=Saved conversation IDs to add to a collection (required\\, non-empty\\, for 'add_collection_members'; rejected by 'create_collection'\\, which always creates an empty collection). Every ID must already be a saved conversation; bookmark it with 'save_conversation' first. Re-adding a member is a no-op."`

	// Experiments.
	ExperimentID string `json:"experiment_id,omitempty" jsonschema:"description=Experiment ID from agento11y_evals_read's 'list_experiments' (required for 'update_experiment' and 'cancel_experiment')"`

	// Test suites.
	SuiteID    string `json:"suite_id,omitempty" jsonschema:"description=Test suite ID from agento11y_evals_read's 'list_suites'. Required for every test-suite write operation except 'create_suite'\\, where it is an optional caller-chosen ID that must not already exist."`
	Version    string `json:"version,omitempty" jsonschema:"description=Suite version such as 'v3'\\, from agento11y_evals_read's 'get_suite'. Required for 'publish_version'\\, 'upsert_test_case'\\, and 'delete_test_case'. 'create_draft_version' assigns the next version itself\\, so do not send one."`
	TestCaseID string `json:"test_case_id,omitempty" jsonschema:"description=Test case ID. Required for 'delete_test_case'. For 'upsert_test_case' it selects an existing case to replace; omit it to have one created."`

	// Shared name/description/tags, used by save_conversation, create/update
	// collection, update_experiment, and the test-suite write operations. Name
	// and Description are pointers so an omitted field (left unchanged on an
	// update) stays distinct from an explicitly blank one.
	Name        *string        `json:"name,omitempty" jsonschema:"description=Human-readable name. Required and non-blank for 'save_conversation'\\, 'create_collection'\\, and 'create_suite'. Optional for 'update_collection'\\, 'update_suite'\\, and 'upsert_test_case' (where it names the case); an omitted field is left unchanged and a blank one is rejected."`
	Description *string        `json:"description,omitempty" jsonschema:"description=Description text (for 'create_collection'\\, 'update_collection'\\, 'update_suite'\\, and 'upsert_test_case'). An omitted field is left unchanged on an update and an explicitly empty string clears it."`
	Tags        *[]string      `json:"tags,omitempty" jsonschema:"description=Replacement tag list for 'update_experiment'\\, 'update_suite'\\, and 'upsert_test_case'. It overwrites the whole list rather than adding to it\\, so read the current tags first; an explicitly empty array clears them. This is not a filter - the read-side experiment tag filter is agento11y_evals_read's 'tag' parameter."`
	Metadata    map[string]any `json:"metadata,omitempty" jsonschema:"description=Free-form metadata object (for 'update_experiment' and 'upsert_test_case'). A finished experiment rejects it with 409."`

	Changelog  string `json:"changelog,omitempty" jsonschema:"description=Note describing what the new version changes (for 'create_draft_version')"`
	EmptyDraft bool   `json:"empty_draft,omitempty" jsonschema:"description=Start the new version with no test cases (for 'create_draft_version'). The default copies every case of the latest published version\\, which is what an edit to an existing suite wants."`

	Category     string                 `json:"category,omitempty" jsonschema:"description=Test case category (for 'upsert_test_case')"`
	Input        map[string]any         `json:"input,omitempty" jsonschema:"description=What the case feeds the agent (required and non-empty for 'upsert_test_case'). The shape is free-form and decided by the runner\\, so copy an existing case of the suite before inventing one."`
	Expected     map[string]any         `json:"expected,omitempty" jsonschema:"description=What the case expects back (for 'upsert_test_case'). Free-form\\, and read by the evaluators rather than compared by the API."`
	ArtifactRefs []Agento11yArtifactRef `json:"artifact_refs,omitempty" jsonschema:"description=Artifacts the case refers to\\, each an artifact_id with its name and kind (for 'upsert_test_case')"`

	// TrialID exists only so update_experiment/cancel_experiment can reject it
	// with a specific error if a caller confuses a trial-scoped read for an
	// experiment-scoped write; no write operation reads it.
	TrialID string `json:"trial_id,omitempty" jsonschema:"description=Not accepted by any write operation - both experiment writes address the experiment\\, not a trial. Present only so a trial ID left over from a read call is rejected with a specific error rather than silently dropped."`
}

// validate tries each of the five sub-domains' own write-operation validators
// in turn, exactly as Agento11yEvalsReadParams.validate() does for reads. If
// none of the five own the operation, it is either a sibling read operation -
// reported with a specific "call agento11y_evals_read instead" error rather
// than accepted - or genuinely unknown. This is the disjoint-operation
// delegation shape from tools/sift_types.go's SiftWriteParams.validate() and
// tools/operation_validation_test.go's TestDelegation_SplitNameDisjointOperations:
// it must never return nil for an operation this tool does not implement.
func (p Agento11yEvalsWriteParams) validate() error {
	if err := p.validateEvaluatorWrite(); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.validateEvalRuleWrite(); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.validateEvalCollectionWrite(); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.validateExperimentWrite(); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}
	if err := p.validateTestSuiteWrite(); !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}

	if isAgento11yEvalsReadOperation(p.Operation) {
		return fmt.Errorf("%q is an agento11y_evals_read operation, not agento11y_evals_write; call agento11y_evals_read instead, or use one of: %s", p.Operation, agento11yEvalsWriteOperations)
	}
	return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yEvalsWriteOperations)
}

// isAgento11yEvalsReadOperation reports whether operation is one of
// agento11y_evals_read's operations, purely to produce a more specific
// "wrong tool" error from Agento11yEvalsWriteParams.validate(); it must never
// be used to accept the operation as valid for agento11y_evals_write.
func isAgento11yEvalsReadOperation(operation string) bool {
	for _, op := range strings.Split(agento11yEvalsReadOperations, ", ") {
		if op == operation {
			return true
		}
	}
	return false
}

func (p Agento11yEvalsWriteParams) validateEvaluatorWrite() error {
	switch p.Operation {
	case "upsert_evaluator":
		if len(p.Definition) == 0 {
			return fmt.Errorf("definition is required for 'upsert_evaluator' operation (it carries evaluator_id, version, kind, and config)")
		}
		return validateAgento11yDefinitionEvaluatorID(p.Definition, p.EvaluatorID, p.Operation)
	case "delete_evaluator":
		if p.EvaluatorID == "" {
			return fmt.Errorf("evaluator_id is required for 'delete_evaluator' operation")
		}
		return nil
	case "fork_template":
		if p.TemplateID == "" {
			return fmt.Errorf("template_id is required for 'fork_template' operation")
		}
		if len(p.Definition) == 0 {
			return fmt.Errorf("definition is required for 'fork_template' operation (it carries the new evaluator_id)")
		}
		return validateAgento11yDefinitionEvaluatorID(p.Definition, p.EvaluatorID, p.Operation)
	case "test_evaluator":
		if len(p.Definition) == 0 {
			return fmt.Errorf("definition is required for 'test_evaluator' operation (it carries kind, config, and output_keys)")
		}
		if p.GenerationID == "" {
			return fmt.Errorf("generation_id is required for 'test_evaluator' operation")
		}
		for _, key := range []string{"evaluator_id", "version"} {
			if _, ok := p.Definition[key]; ok {
				return fmt.Errorf("definition must not contain %q for 'test_evaluator' operation: the API rejects it as an unknown field, since a test run stores nothing (use 'upsert_evaluator' to store an evaluator)", key)
			}
		}
		return nil
	default:
		return errAgento11yUnknownOperation
	}
}

func (p Agento11yEvalsWriteParams) validateEvalRuleWrite() error {
	switch p.Operation {
	case "create_rule", "create_guard", "preview_rule":
		if len(p.Definition) == 0 {
			return fmt.Errorf("definition is required for %q operation", p.Operation)
		}
		return nil
	case "update_rule", "update_guard":
		if p.RuleID == "" {
			return fmt.Errorf("rule_id is required for %q operation", p.Operation)
		}
		if len(p.Definition) == 0 {
			return fmt.Errorf("definition is required for %q operation", p.Operation)
		}
		return nil
	case "delete_rule", "delete_guard":
		if p.RuleID == "" {
			return fmt.Errorf("rule_id is required for %q operation", p.Operation)
		}
		return nil
	default:
		return errAgento11yUnknownOperation
	}
}

func (p Agento11yEvalsWriteParams) validateEvalCollectionWrite() error {
	switch p.Operation {
	case "save_conversation":
		if p.ConversationID == "" {
			return fmt.Errorf("conversation_id is required for 'save_conversation' operation")
		}
		if p.CollectionID != "" {
			return fmt.Errorf("collection_id is not accepted by 'save_conversation' operation: bookmarking and adding to a collection are two calls, so run 'save_conversation' first and then 'add_collection_members' with the resulting saved_id")
		}
		if p.Name == nil || strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name is required for 'save_conversation' operation")
		}
		// Both the supplied and the derived ID are checked here, so an invalid
		// ID fails with the rule spelled out instead of a bare 400 from the API.
		if p.SavedID != "" {
			if !agento11ySavedIDPattern.MatchString(p.SavedID) {
				return fmt.Errorf("saved_id %q is invalid: only letters, digits, '_', '.', ':', and '-' are accepted", p.SavedID)
			}
			return nil
		}
		if derived := agento11yDerivedSavedID(p.ConversationID); !agento11ySavedIDPattern.MatchString(derived) {
			return fmt.Errorf("the saved_id derived from conversation_id, %q, is invalid: only letters, digits, '_', '.', ':', and '-' are accepted, so pass an explicit saved_id", derived)
		}
		return nil
	case "delete_saved_conversation":
		if p.SavedID == "" {
			return fmt.Errorf("saved_id is required for 'delete_saved_conversation' operation")
		}
		return nil
	case "create_collection":
		if p.Name == nil || strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name is required for 'create_collection' operation")
		}
		// The API assigns the ID, so a supplied one would be dropped and the
		// caller would reuse an ID the collection does not have.
		if p.CollectionID != "" {
			return fmt.Errorf("collection_id is not accepted by 'create_collection' operation, which gets a server-assigned UUID: use the collection_id from the response for follow-up calls")
		}
		// The API creates an empty collection; these would be dropped silently
		// and the caller would be told the members are in it.
		if len(p.SavedIDs) > 0 {
			return fmt.Errorf("saved_ids is not accepted by 'create_collection' operation, which always creates an empty collection: run 'add_collection_members' with the returned collection_id to fill it")
		}
		return nil
	case "update_collection":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'update_collection' operation")
		}
		if p.Name == nil && p.Description == nil {
			return fmt.Errorf("at least one of name or description is required for 'update_collection' operation (an omitted field is left unchanged)")
		}
		if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name must not be blank for 'update_collection' operation (omit it to leave the name unchanged)")
		}
		return nil
	case "delete_collection":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'delete_collection' operation")
		}
		return nil
	case "add_collection_members":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'add_collection_members' operation")
		}
		if len(p.SavedIDs) == 0 {
			return fmt.Errorf("saved_ids is required for 'add_collection_members' operation and must contain at least one saved conversation ID")
		}
		return nil
	case "remove_collection_member":
		if p.CollectionID == "" {
			return fmt.Errorf("collection_id is required for 'remove_collection_member' operation")
		}
		if p.SavedID == "" {
			return fmt.Errorf("saved_id is required for 'remove_collection_member' operation")
		}
		return nil
	default:
		return errAgento11yUnknownOperation
	}
}

func (p Agento11yEvalsWriteParams) validateExperimentWrite() error {
	switch p.Operation {
	case "update_experiment":
		if p.ExperimentID == "" {
			return fmt.Errorf("experiment_id is required for 'update_experiment' operation")
		}
		if p.Name == nil && p.Description == nil && p.Tags == nil && p.Metadata == nil {
			return fmt.Errorf("at least one of name, description, tags, or metadata is required for 'update_experiment' operation (an omitted field is left unchanged)")
		}
		if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name must not be blank for 'update_experiment' operation (omit it to leave the name unchanged)")
		}
		return p.rejectExperimentTrialID()
	case "cancel_experiment":
		if p.ExperimentID == "" {
			return fmt.Errorf("experiment_id is required for 'cancel_experiment' operation")
		}
		// The cancel route sends no body, so a field meant for update_experiment
		// is dropped on the way out while the experiment still stops.
		if field := p.experimentUpdateOnlyField(); field != "" {
			return fmt.Errorf("%s is not accepted by 'cancel_experiment' operation, which would drop it: it is only read by 'update_experiment'", field)
		}
		return p.rejectExperimentTrialID()
	default:
		return errAgento11yUnknownOperation
	}
}

// rejectExperimentTrialID refuses a trial ID on either experiment write. Both
// address an experiment; a trial ID is dropped, so 'cancel_experiment' with
// one reads as trial-scoped and stops every trial of the run instead, and
// still reports success.
func (p Agento11yEvalsWriteParams) rejectExperimentTrialID() error {
	if p.TrialID == "" {
		return nil
	}
	return agento11yUnusedParamError("trial_id", p.Operation, []string{"get_trial", "list_trial_scores", "list_trial_artifacts"})
}

// experimentUpdateOnlyField reports the first body field that is set and that
// only update_experiment sends.
func (p Agento11yEvalsWriteParams) experimentUpdateOnlyField() string {
	switch {
	case p.Name != nil:
		return "name"
	case p.Description != nil:
		return "description"
	case p.Tags != nil:
		return "tags"
	case p.Metadata != nil:
		return "metadata"
	default:
		return ""
	}
}

func (p Agento11yEvalsWriteParams) validateTestSuiteWrite() error {
	switch p.Operation {
	case "create_suite":
		if p.Name == nil || strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name is required for 'create_suite' operation")
		}
	case "update_suite":
		if err := agento11yRequireSuiteID(p.Operation, p.SuiteID); err != nil {
			return err
		}
		if p.Name == nil && p.Description == nil && p.Tags == nil {
			return fmt.Errorf("at least one of name, description, or tags is required for 'update_suite' operation (an omitted field is left unchanged)")
		}
		if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name must not be blank for 'update_suite' operation (omit it to leave the name unchanged)")
		}
	case "create_draft_version":
		if err := agento11yRequireSuiteID(p.Operation, p.SuiteID); err != nil {
			return err
		}
		if p.Version != "" {
			return fmt.Errorf("version must not be set for 'create_draft_version' operation: the API assigns the next version itself")
		}
	case "publish_version":
		if err := agento11yRequireVersion(p.Operation, p.SuiteID, p.Version); err != nil {
			return err
		}
	case "upsert_test_case":
		if err := agento11yRequireVersion(p.Operation, p.SuiteID, p.Version); err != nil {
			return err
		}
		if len(p.Input) == 0 {
			return fmt.Errorf("input is required and must not be empty for 'upsert_test_case' operation")
		}
	case "delete_test_case":
		if err := agento11yRequireVersion(p.Operation, p.SuiteID, p.Version); err != nil {
			return err
		}
		if p.TestCaseID == "" {
			return fmt.Errorf("test_case_id is required for 'delete_test_case' operation")
		}
	default:
		return errAgento11yUnknownOperation
	}

	// Each write route sends its own body, so a field belonging to a sibling
	// test-suite operation is refused rather than dropped on the way out.
	if field, readBy := p.unusedTestSuiteBodyField(); field != "" {
		return agento11yUnusedParamError(field, p.Operation, readBy)
	}
	if field, readBy := p.unusedTestSuiteWriteIDField(); field != "" {
		return agento11yUnusedParamError(field, p.Operation, readBy)
	}
	return nil
}

func (p Agento11yEvalsWriteParams) unusedTestSuiteBodyField() (string, []string) {
	// A suite and a test case both carry a name, a description, and tags.
	suiteAndCase := []string{"create_suite", "update_suite", "upsert_test_case"}
	caseOnly := []string{"upsert_test_case"}
	draftOnly := []string{"create_draft_version"}

	return agento11yFirstUnusedParam(p.Operation, []agento11yParamUse{
		{name: "name", set: p.Name != nil, operations: suiteAndCase},
		{name: "description", set: p.Description != nil, operations: suiteAndCase},
		{name: "tags", set: p.Tags != nil, operations: suiteAndCase},
		{name: "changelog", set: p.Changelog != "", operations: draftOnly},
		{name: "empty_draft", set: p.EmptyDraft, operations: draftOnly},
		{name: "category", set: p.Category != "", operations: caseOnly},
		{name: "input", set: p.Input != nil, operations: caseOnly},
		{name: "expected", set: p.Expected != nil, operations: caseOnly},
		{name: "metadata", set: p.Metadata != nil, operations: caseOnly},
		{name: "artifact_refs", set: p.ArtifactRefs != nil, operations: caseOnly},
	})
}

// unusedTestSuiteWriteIDField reports an ID that a test-suite write operation
// does not address. 'create_draft_version' rejects a version with its own
// message above, so the version it assigns is explained rather than reported
// generically here.
func (p Agento11yEvalsWriteParams) unusedTestSuiteWriteIDField() (string, []string) {
	return agento11yFirstUnusedParam(p.Operation, []agento11yParamUse{
		{name: "version", set: p.Version != "", operations: []string{"publish_version", "upsert_test_case", "delete_test_case"}},
		{name: "test_case_id", set: p.TestCaseID != "", operations: []string{"upsert_test_case", "delete_test_case"}},
	})
}

// evaluatorWriteRequest, evalRuleWriteRequest, evalCollectionWriteRequest,
// experimentWriteRequest, and testSuiteWriteRequest assemble the input each
// sub-domain's write dispatcher expects from the flat params. They are only
// called once validate() has confirmed the operation belongs to that
// sub-domain, so they do not re-check anything themselves.

func (p Agento11yEvalsWriteParams) evaluatorWriteRequest() agento11yEvaluatorWriteRequest {
	return agento11yEvaluatorWriteRequest{
		EvaluatorID:  p.EvaluatorID,
		TemplateID:   p.TemplateID,
		Definition:   p.Definition,
		GenerationID: p.GenerationID,
	}
}

func (p Agento11yEvalsWriteParams) evalRuleWriteRequest() agento11yEvalRuleWriteRequest {
	return agento11yEvalRuleWriteRequest{
		RuleID:     p.RuleID,
		Definition: p.Definition,
	}
}

func (p Agento11yEvalsWriteParams) evalCollectionWriteRequest() agento11yEvalCollectionWriteRequest {
	return agento11yEvalCollectionWriteRequest{
		SavedID:        p.SavedID,
		CollectionID:   p.CollectionID,
		ConversationID: p.ConversationID,
		Name:           derefAgento11yString(p.Name),
		Description:    p.Description,
		Tags:           p.ConversationTags,
		SavedIDs:       p.SavedIDs,
	}
}

func (p Agento11yEvalsWriteParams) experimentWriteRequest() agento11yExperimentWriteRequest {
	return agento11yExperimentWriteRequest{
		ExperimentID: p.ExperimentID,
		Name:         p.Name,
		Description:  p.Description,
		Tags:         p.Tags,
		Metadata:     p.Metadata,
	}
}

func (p Agento11yEvalsWriteParams) testSuiteWriteRequest() agento11yTestSuiteWriteRequest {
	return agento11yTestSuiteWriteRequest{
		SuiteID:      p.SuiteID,
		Version:      p.Version,
		TestCaseID:   p.TestCaseID,
		Name:         p.Name,
		Description:  p.Description,
		Tags:         p.Tags,
		Changelog:    p.Changelog,
		EmptyDraft:   p.EmptyDraft,
		Category:     p.Category,
		Input:        p.Input,
		Expected:     p.Expected,
		Metadata:     p.Metadata,
		ArtifactRefs: p.ArtifactRefs,
	}
}

// derefAgento11yString returns the dereferenced value of s, or "" if s is nil.
func derefAgento11yString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
