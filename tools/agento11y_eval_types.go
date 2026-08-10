package tools

import (
	"errors"
	"fmt"
	"time"
)

// Wire types for the Agent Observability eval control plane
// (/eval/* on the grafana-agento11y-app plugin resources proxy). Only the
// fields the plugin actually returns are declared; nested config bodies stay
// map[string]any because their shape depends on the evaluator kind.

// Agento11yEvaluatorDefinition is an evaluator from /eval/evaluators.
type Agento11yEvaluatorDefinition struct {
	EvaluatorID string               `json:"evaluator_id"`
	Version     string               `json:"version"`
	Kind        string               `json:"kind"` // llm_judge, json_schema, regex, heuristic
	Description string               `json:"description,omitempty"`
	Config      map[string]any       `json:"config,omitempty"`
	OutputKeys  []Agento11yOutputKey `json:"output_keys,omitempty"`

	TenantID              string     `json:"tenant_id,omitempty"`
	IsPredefined          bool       `json:"is_predefined,omitempty"`
	SourceTemplateID      string     `json:"source_template_id,omitempty"`
	SourceTemplateVersion string     `json:"source_template_version,omitempty"`
	CreatedBy             string     `json:"created_by,omitempty"`
	UpdatedBy             string     `json:"updated_by,omitempty"`
	DeletedAt             *time.Time `json:"deleted_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at,omitzero"`
	UpdatedAt             time.Time  `json:"updated_at,omitzero"`
}

// Agento11yOutputKey describes one score an evaluator emits.
type Agento11yOutputKey struct {
	Key           string   `json:"key"`
	Type          string   `json:"type"`
	Description   string   `json:"description,omitempty"`
	Unit          string   `json:"unit,omitempty"`
	PassThreshold *float64 `json:"pass_threshold,omitempty"`
	Enum          []string `json:"enum,omitempty"`
	Min           *float64 `json:"min,omitempty"`
	Max           *float64 `json:"max,omitempty"`
	PassMatch     []string `json:"pass_match,omitempty"`
	PassValue     *bool    `json:"pass_value,omitempty"`
}

// Agento11yTemplateDefinition is a list item from /eval/templates.
type Agento11yTemplateDefinition struct {
	TemplateID    string     `json:"template_id"`
	Scope         string     `json:"scope"` // global, tenant
	Kind          string     `json:"kind"`
	LatestVersion string     `json:"latest_version,omitempty"`
	Description   string     `json:"description,omitempty"`
	TenantID      string     `json:"tenant_id,omitempty"`
	CreatedBy     string     `json:"created_by,omitempty"`
	UpdatedBy     string     `json:"updated_by,omitempty"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at,omitzero"`
	UpdatedAt     time.Time  `json:"updated_at,omitzero"`
}

// Agento11yTemplateVersion is a version item from /eval/templates/{id}/versions.
type Agento11yTemplateVersion struct {
	TemplateID string               `json:"template_id"`
	Version    string               `json:"version"`
	Config     map[string]any       `json:"config,omitempty"`
	OutputKeys []Agento11yOutputKey `json:"output_keys,omitempty"`
	Changelog  string               `json:"changelog,omitempty"`
	CreatedBy  string               `json:"created_by,omitempty"`
	UpdatedBy  string               `json:"updated_by,omitempty"`
	CreatedAt  time.Time            `json:"created_at,omitzero"`
	UpdatedAt  time.Time            `json:"updated_at,omitzero"`
}

// Agento11yJudgeProvider is a provider from /eval/judge/providers.
type Agento11yJudgeProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Agento11yJudgeModel is a model from /eval/judge/models.
type Agento11yJudgeModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	ContextWindow int    `json:"context_window"`
}

// Agento11yJudgeProvidersResponse is the judge providers envelope. The judge
// endpoints do not use the {items, next_cursor} envelope the other eval list
// routes return.
type Agento11yJudgeProvidersResponse struct {
	Providers []Agento11yJudgeProvider `json:"providers"`
}

// Agento11yJudgeModelsResponse is the judge models envelope.
type Agento11yJudgeModelsResponse struct {
	Models []Agento11yJudgeModel `json:"models"`
}

// errAgento11yUnknownOperation is returned by the shared operation validators
// and dispatchers when an operation is not one they handle, so each tool variant
// can report the operation set it actually advertises.
var errAgento11yUnknownOperation = errors.New("unknown agento11y eval operation")

// agento11yEvaluatorFields are the filter and pagination parameters shared by
// the read and read-write variants of agento11y_manage_evaluators. The ID
// parameters are declared on each variant separately, because their guidance
// names the operations that use them and the read variant must not advertise
// operations it rejects.
type agento11yEvaluatorFields struct {
	Scope    string `json:"scope,omitempty" jsonschema:"enum=global,enum=tenant,description=Template scope filter: 'global' for built-in templates\\, 'tenant' for templates created in this tenant (for 'list_templates')"`
	Provider string `json:"provider,omitempty" jsonschema:"description=Judge provider ID to filter models by\\, e.g. 'openai' (for 'list_judge_models')"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max 500) (for the paginated list operations)"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor (for the paginated list operations)"`
}

// agento11yEvaluatorReadRequest is the input of an evaluator read operation,
// assembled by either tool variant.
type agento11yEvaluatorReadRequest struct {
	agento11yEvaluatorFields

	EvaluatorID string
	TemplateID  string
}

// validateOperation validates the read operations of
// agento11y_manage_evaluators. Operations it does not handle return
// errAgento11yUnknownOperation.
func (r agento11yEvaluatorReadRequest) validateOperation(operation string) error {
	switch operation {
	case "list_evaluators", "list_templates", "list_judge_providers", "list_judge_models":
		return nil
	case "get_evaluator":
		if r.EvaluatorID == "" {
			return fmt.Errorf("evaluator_id is required for 'get_evaluator' operation")
		}
		return nil
	case "get_template", "list_template_versions":
		if r.TemplateID == "" {
			return fmt.Errorf("template_id is required for %q operation", operation)
		}
		return nil
	default:
		return errAgento11yUnknownOperation
	}
}

// ManageAgento11yEvaluatorsReadParams is the param struct for the read-only
// version of agento11y_manage_evaluators.
type ManageAgento11yEvaluatorsReadParams struct {
	agento11yEvaluatorFields

	Operation   string `json:"operation" jsonschema:"required,enum=list_evaluators,enum=get_evaluator,enum=list_templates,enum=get_template,enum=list_template_versions,enum=list_judge_providers,enum=list_judge_models,description=The operation to perform: 'list_evaluators' for the evaluators in this tenant\\, 'get_evaluator' for one evaluator's kind/config/output_keys\\, 'list_templates' for evaluator templates\\, 'get_template' for one template with its config and versions\\, 'list_template_versions' for a template's version history\\, 'list_judge_providers' for configured judge providers\\, 'list_judge_models' for the judge models of a provider"`
	EvaluatorID string `json:"evaluator_id,omitempty" jsonschema:"description=Evaluator ID (required for 'get_evaluator')"`
	TemplateID  string `json:"template_id,omitempty" jsonschema:"description=Template ID (required for 'get_template' and 'list_template_versions')"`
}

func (p ManageAgento11yEvaluatorsReadParams) readRequest() agento11yEvaluatorReadRequest {
	return agento11yEvaluatorReadRequest{
		agento11yEvaluatorFields: p.agento11yEvaluatorFields,
		EvaluatorID:              p.EvaluatorID,
		TemplateID:               p.TemplateID,
	}
}

func (p ManageAgento11yEvaluatorsReadParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return fmt.Errorf("unknown operation %q, must be one of: list_evaluators, get_evaluator, list_templates, get_template, list_template_versions, list_judge_providers, list_judge_models", p.Operation)
	}
	return err
}

// ManageAgento11yEvaluatorsReadWriteParams is the param struct for the
// read-write version of agento11y_manage_evaluators.
type ManageAgento11yEvaluatorsReadWriteParams struct {
	agento11yEvaluatorFields

	Operation    string         `json:"operation" jsonschema:"required,enum=list_evaluators,enum=get_evaluator,enum=list_templates,enum=get_template,enum=list_template_versions,enum=list_judge_providers,enum=list_judge_models,enum=upsert_evaluator,enum=delete_evaluator,enum=fork_template,enum=test_evaluator,description=The operation to perform. Reads: 'list_evaluators'\\, 'get_evaluator'\\, 'list_templates'\\, 'get_template'\\, 'list_template_versions'\\, 'list_judge_providers'\\, 'list_judge_models'. Writes: 'upsert_evaluator' (create or update from 'definition')\\, 'delete_evaluator'\\, 'fork_template' (derive an evaluator from a template)\\, 'test_evaluator' (score one generation with an inline definition without storing it)"`
	EvaluatorID  string         `json:"evaluator_id,omitempty" jsonschema:"description=Evaluator ID (required for 'get_evaluator' and 'delete_evaluator'). For 'upsert_evaluator' and 'fork_template' the evaluator ID belongs in 'definition' instead\\, because the API reads it from the request body."`
	TemplateID   string         `json:"template_id,omitempty" jsonschema:"description=Template ID (required for 'get_template'\\, 'list_template_versions'\\, and 'fork_template')"`
	Definition   map[string]any `json:"definition,omitempty" jsonschema:"description=Request body for 'upsert_evaluator'\\, 'fork_template'\\, and 'test_evaluator'. For 'upsert_evaluator': evaluator_id (string\\, required\\, only letters/digits/underscore/dot - hyphens are rejected)\\, version (string\\, required - re-using an existing version returns HTTP 409\\, so bump it to change an evaluator)\\, kind (llm_judge|json_schema|regex|heuristic)\\, description (string)\\, config (object\\, shape depends on kind)\\, output_keys (array of {key\\, type\\, description\\, unit\\, pass_threshold\\, enum\\, min\\, max\\, pass_match\\, pass_value}). The evaluator ID lives in this body\\, not in a URL or the evaluator_id parameter. For 'fork_template': evaluator_id (required) plus optional version (which template version to fork\\, defaults to the template's latest)\\, config\\, and output_keys overrides. For 'test_evaluator': kind\\, config\\, and output_keys of the evaluator to try - the API rejects evaluator_id and version here\\, and nothing is persisted."`
	GenerationID string         `json:"generation_id,omitempty" jsonschema:"description=Generation to score (required for 'test_evaluator'). Find one with agento11y_manage_conversations."`
}

func (p ManageAgento11yEvaluatorsReadWriteParams) readRequest() agento11yEvaluatorReadRequest {
	return agento11yEvaluatorReadRequest{
		agento11yEvaluatorFields: p.agento11yEvaluatorFields,
		EvaluatorID:              p.EvaluatorID,
		TemplateID:               p.TemplateID,
	}
}

func (p ManageAgento11yEvaluatorsReadWriteParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}

	switch p.Operation {
	case "upsert_evaluator":
		if len(p.Definition) == 0 {
			return fmt.Errorf("definition is required for 'upsert_evaluator' operation (it carries evaluator_id, version, kind, and config)")
		}
		return p.validateDefinitionEvaluatorID()
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
		return p.validateDefinitionEvaluatorID()
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
		return fmt.Errorf("unknown operation %q, must be one of: list_evaluators, get_evaluator, list_templates, get_template, list_template_versions, list_judge_providers, list_judge_models, upsert_evaluator, delete_evaluator, fork_template, test_evaluator", p.Operation)
	}
}

// validateDefinitionEvaluatorID checks the evaluator ID of the operations that
// carry it in the request body. The API reads the ID from the body, so a
// top-level evaluator_id never reaches the server and would otherwise be
// dropped without a word.
func (p ManageAgento11yEvaluatorsReadWriteParams) validateDefinitionEvaluatorID() error {
	id, _ := p.Definition["evaluator_id"].(string)
	if id == "" {
		return fmt.Errorf("definition.evaluator_id (a non-empty string) is required for %q operation: the API takes the evaluator ID from the definition body, not from the evaluator_id parameter", p.Operation)
	}
	if p.EvaluatorID != "" && p.EvaluatorID != id {
		return fmt.Errorf("evaluator_id %q conflicts with definition.evaluator_id %q for %q operation: set the evaluator ID in the definition only", p.EvaluatorID, id, p.Operation)
	}
	return nil
}

// agento11yEvalRuleFields are the pagination parameters shared by the read and
// read-write variants of agento11y_manage_eval_rules. The ID parameter is
// declared on each variant separately, because its guidance names the
// operations that use it and the read variant must not advertise operations it
// rejects.
type agento11yEvalRuleFields struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max 500) (for 'list_rules' and 'list_guards')"`
	Cursor string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor (for 'list_rules' and 'list_guards')"`
}

// agento11yEvalRuleReadRequest is the input of a rule or guard read operation,
// assembled by either tool variant.
type agento11yEvalRuleReadRequest struct {
	agento11yEvalRuleFields

	RuleID string
}

// validateOperation validates the read operations of
// agento11y_manage_eval_rules. Operations it does not handle return
// errAgento11yUnknownOperation.
func (r agento11yEvalRuleReadRequest) validateOperation(operation string) error {
	switch operation {
	case "list_rules", "list_guards":
		return nil
	case "get_rule", "get_guard":
		if r.RuleID == "" {
			return fmt.Errorf("rule_id is required for %q operation", operation)
		}
		return nil
	default:
		return errAgento11yUnknownOperation
	}
}

// ManageAgento11yEvalRulesReadParams is the param struct for the read-only
// version of agento11y_manage_eval_rules.
type ManageAgento11yEvalRulesReadParams struct {
	agento11yEvalRuleFields

	Operation string `json:"operation" jsonschema:"required,enum=list_rules,enum=get_rule,enum=list_guards,enum=get_guard,description=The operation to perform: 'list_rules' for the asynchronous eval rules in this tenant\\, 'get_rule' for one eval rule\\, 'list_guards' for the inline guards (hook rules)\\, 'get_guard' for one guard"`
	RuleID    string `json:"rule_id,omitempty" jsonschema:"description=Eval rule ID (for 'get_rule') or guard ID (for 'get_guard'). Required for both."`
}

func (p ManageAgento11yEvalRulesReadParams) readRequest() agento11yEvalRuleReadRequest {
	return agento11yEvalRuleReadRequest{
		agento11yEvalRuleFields: p.agento11yEvalRuleFields,
		RuleID:                  p.RuleID,
	}
}

func (p ManageAgento11yEvalRulesReadParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return fmt.Errorf("unknown operation %q, must be one of: list_rules, get_rule, list_guards, get_guard", p.Operation)
	}
	return err
}

// ManageAgento11yEvalRulesReadWriteParams is the param struct for the
// read-write version of agento11y_manage_eval_rules.
type ManageAgento11yEvalRulesReadWriteParams struct {
	agento11yEvalRuleFields

	Operation  string         `json:"operation" jsonschema:"required,enum=list_rules,enum=get_rule,enum=list_guards,enum=get_guard,enum=create_rule,enum=update_rule,enum=delete_rule,enum=preview_rule,enum=create_guard,enum=update_guard,enum=delete_guard,description=The operation to perform. Reads: 'list_rules'\\, 'get_rule'\\, 'list_guards'\\, 'get_guard'. Writes: 'create_rule'\\, 'update_rule' (PATCH\\, partial)\\, 'delete_rule'\\, 'preview_rule' (dry-run a selector and match against recent traffic\\, stores nothing)\\, 'create_guard'\\, 'update_guard' (PUT\\, full replace)\\, 'delete_guard'"`
	RuleID     string         `json:"rule_id,omitempty" jsonschema:"description=Eval rule ID (for the '_rule' operations) or guard ID (for the '_guard' operations). Required for 'get_rule'\\, 'update_rule'\\, 'delete_rule'\\, 'get_guard'\\, 'update_guard'\\, and 'delete_guard'; for 'create_rule'\\, 'create_guard'\\, and 'preview_rule' the ID comes from 'definition'."`
	Definition map[string]any `json:"definition,omitempty" jsonschema:"description=Request body for the create/update/preview operations. Eval rules ('create_rule'\\, 'update_rule'): rule_id (string\\, required on create\\, only letters/digits/underscore/dot)\\, enabled (bool)\\, selector (user_visible_turn|all_assistant_generations|tool_call_steps|errored_generations|conversation)\\, match (object of string arrays\\, e.g. {\"agent_name\": [\"my-agent\"]})\\, sample_rate (0-1)\\, evaluator_ids (array\\, the evaluators must already exist)\\, alert_rule_uids (array)\\, min_idle_seconds (int\\, required when selector is 'conversation': the idle period after which a conversation counts as finished; with that selector sample_rate applies per conversation\\, not per generation). 'update_rule' is a PATCH: send only the fields to change and do NOT include rule_id\\, which the API rejects as an unknown field - it is taken from the rule_id parameter. 'preview_rule': selector\\, match\\, sample_rate\\, and optionally rule_id. Guards ('create_guard'\\, 'update_guard'): rule_id (required on create; on update it must match the rule_id parameter)\\, enabled (bool)\\, phase (preflight|postflight\\, default preflight)\\, priority (int\\, lower runs first)\\, selector (as above\\, plus 'all')\\, match (as above)\\, action_on_fail (warn|deny - start with warn\\, which records the outcome without blocking the request\\, and promote to deny only after watching the false-positive rate)\\, short_circuit (bool)\\, and exactly ONE decision shape: evaluator_ids (an evaluator decides)\\, redact ({patterns: [{id\\, regex}]} - each entry is exactly id and regex\\, a 'replacement' key returns 400\\, and the placeholder is derived from id) or tool_filter ({blocked_names: [glob]}). 'update_guard' is a PUT full replace: fields you omit reset to server defaults\\, so send a complete definition (normally a 'get_guard' result with your edits)."`
}

func (p ManageAgento11yEvalRulesReadWriteParams) readRequest() agento11yEvalRuleReadRequest {
	return agento11yEvalRuleReadRequest{
		agento11yEvalRuleFields: p.agento11yEvalRuleFields,
		RuleID:                  p.RuleID,
	}
}

func (p ManageAgento11yEvalRulesReadWriteParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if !errors.Is(err, errAgento11yUnknownOperation) {
		return err
	}

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
		return fmt.Errorf("unknown operation %q, must be one of: list_rules, get_rule, list_guards, get_guard, create_rule, update_rule, delete_rule, preview_rule, create_guard, update_guard, delete_guard", p.Operation)
	}
}

// Agento11yRuleDefinition is an asynchronous eval rule from /eval/rules.
type Agento11yRuleDefinition struct {
	RuleID        string         `json:"rule_id"`
	Enabled       bool           `json:"enabled"`
	Selector      string         `json:"selector"` // user_visible_turn, all_assistant_generations, tool_call_steps, errored_generations, conversation
	Match         map[string]any `json:"match,omitempty"`
	SampleRate    float64        `json:"sample_rate"`
	EvaluatorIDs  []string       `json:"evaluator_ids"`
	AlertRuleUIDs []string       `json:"alert_rule_uids,omitempty"`
	// MinIdleSeconds is required by the backend when Selector is "conversation":
	// the idle period after which a conversation counts as complete and becomes
	// eligible for evaluation.
	MinIdleSeconds *int `json:"min_idle_seconds,omitempty"`

	TenantID  string     `json:"tenant_id,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	CreatedAt time.Time  `json:"created_at,omitzero"`
	UpdatedAt time.Time  `json:"updated_at,omitzero"`
}

// Agento11yHookRuleDefinition is a guard from /eval/hook-rules. Guards run
// inline on the request path, unlike the asynchronous rules in /eval/rules.
// Exactly one of EvaluatorIDs, ToolFilter, or Redact decides the outcome; the
// server rejects a guard that sets none or several.
type Agento11yHookRuleDefinition struct {
	RuleID       string                     `json:"rule_id"`
	Enabled      bool                       `json:"enabled"`
	Phase        string                     `json:"phase"` // preflight, postflight
	Priority     int                        `json:"priority"`
	Selector     string                     `json:"selector"` // user_visible_turn, all_assistant_generations, tool_call_steps, errored_generations, all
	Match        map[string]any             `json:"match,omitempty"`
	EvaluatorIDs []string                   `json:"evaluator_ids,omitempty"`
	ActionOnFail string                     `json:"action_on_fail"` // deny, warn
	ShortCircuit bool                       `json:"short_circuit"`
	ToolFilter   *Agento11yToolFilterConfig `json:"tool_filter,omitempty"`
	Redact       *Agento11yTransformConfig  `json:"redact,omitempty"`

	TenantID  string     `json:"tenant_id,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	CreatedAt time.Time  `json:"created_at,omitzero"`
	UpdatedAt time.Time  `json:"updated_at,omitzero"`
}

// Agento11yToolFilterConfig blocks tool calls by name glob.
type Agento11yToolFilterConfig struct {
	BlockedNames []string `json:"blocked_names"`
}

// Agento11yTransformConfig redacts generation content by regex. The server
// derives the placeholder from the pattern id; there is no caller-supplied
// replacement string.
type Agento11yTransformConfig struct {
	Patterns []Agento11yTransformPattern `json:"patterns"`
}

// Agento11yTransformPattern is one regex applied by a redact guard. The
// server's schema is exactly {id, regex}; any other field (such as
// "replacement") is rejected with 400.
type Agento11yTransformPattern struct {
	ID    string `json:"id,omitempty"`
	Regex string `json:"regex"`
}

// Agento11yEvalTestResponse is the response from POST /eval:test.
type Agento11yEvalTestResponse struct {
	GenerationID    string                   `json:"generation_id"`
	ConversationID  string                   `json:"conversation_id,omitempty"`
	Scores          []Agento11yEvalTestScore `json:"scores"`
	ExecutionTimeMs int64                    `json:"execution_time_ms"`
}

// Agento11yEvalTestScore is one score produced by a test run.
type Agento11yEvalTestScore struct {
	Key         string         `json:"key"`
	Type        string         `json:"type"`
	Value       any            `json:"value"`
	Passed      *bool          `json:"passed,omitempty"`
	Explanation string         `json:"explanation,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Agento11yRulePreviewResponse is the response from POST /eval/rules:preview.
type Agento11yRulePreviewResponse struct {
	WindowHours         int                                `json:"window_hours"`
	TotalGenerations    int                                `json:"total_generations"`
	ScannedGenerations  int                                `json:"scanned_generations"`
	MatchingGenerations int                                `json:"matching_generations"`
	SampledGenerations  int                                `json:"sampled_generations"`
	Samples             []Agento11yPreviewGenerationSample `json:"samples"`
}

// Agento11yPreviewGenerationSample is one example generation a previewed rule
// would have matched.
type Agento11yPreviewGenerationSample struct {
	GenerationID   string            `json:"generation_id"`
	ConversationID string            `json:"conversation_id"`
	AgentName      string            `json:"agent_name,omitempty"`
	Model          string            `json:"model,omitempty"`
	CreatedAt      string            `json:"created_at"`
	InputPreview   string            `json:"input_preview,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}
