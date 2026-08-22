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
// the read and write variants of agento11y_evals_read/agento11y_evals_write. The ID
// parameters are declared on each variant separately, because their guidance
// names the operations that use them and the read variant must not advertise
// operations it rejects.
type agento11yEvaluatorFields struct {
	Scope    string `json:"scope,omitempty" jsonschema:"enum=global,enum=tenant,description=Template scope filter: 'global' for built-in templates\\, 'tenant' for templates created in this tenant (for 'list_templates')"`
	Provider string `json:"provider,omitempty" jsonschema:"description=Judge provider ID to filter models by\\, e.g. 'openai' (for 'list_judge_models')"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max 500) (for the paginated list operations)"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor (for the paginated list operations)"`
}

const agento11yEvaluatorReadOperations = "list_evaluators, get_evaluator, list_templates, get_template, list_template_versions, list_judge_providers, list_judge_models"

// agento11yEvaluatorReadRequest is the input of an evaluator read operation,
// assembled by either tool variant.
type agento11yEvaluatorReadRequest struct {
	agento11yEvaluatorFields

	EvaluatorID string
	TemplateID  string
}

// validateOperation validates the read operations of
// agento11y_evals_read/agento11y_evals_write. Operations it does not handle return
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

// validateDefinitionEvaluatorID checks the evaluator ID of the operations that
// carry it in the request body. The API reads the ID from the body, so a
// top-level evaluator_id never reaches the server and would otherwise be
// dropped without a word. Shared by agento11y_evals_write's 'upsert_evaluator'
// and 'fork_template' handling in agento11y_evals_types.go.
func validateAgento11yDefinitionEvaluatorID(definition map[string]any, evaluatorID, operation string) error {
	id, _ := definition["evaluator_id"].(string)
	if id == "" {
		return fmt.Errorf("definition.evaluator_id (a non-empty string) is required for %q operation: the API takes the evaluator ID from the definition body, not from the evaluator_id parameter", operation)
	}
	if evaluatorID != "" && evaluatorID != id {
		return fmt.Errorf("evaluator_id %q conflicts with definition.evaluator_id %q for %q operation: set the evaluator ID in the definition only", evaluatorID, id, operation)
	}
	return nil
}

// agento11yEvalRuleFields are the pagination parameters shared by the read and
// write variants of agento11y_evals_read/agento11y_evals_write. The ID parameter is
// declared on each variant separately, because its guidance names the
// operations that use it and the read variant must not advertise operations it
// rejects.
type agento11yEvalRuleFields struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max 500) (for 'list_rules' and 'list_guards')"`
	Cursor string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor (for 'list_rules' and 'list_guards')"`
}

const agento11yEvalRuleReadOperations = "list_rules, get_rule, list_guards, get_guard"

// agento11yEvalRuleReadRequest is the input of a rule or guard read operation,
// assembled by either tool variant.
type agento11yEvalRuleReadRequest struct {
	agento11yEvalRuleFields

	RuleID string
}

// validateOperation validates the read operations of
// agento11y_evals_read/agento11y_evals_write. Operations it does not handle return
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
