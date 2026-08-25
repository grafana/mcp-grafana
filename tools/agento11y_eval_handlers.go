package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// agento11yPageQuery builds the query for a paginated eval list route. The
// limit is always sent explicitly so the page size is visible in the request
// rather than relying on the upstream default.
func agento11yPageQuery(limit int, cursor string) url.Values {
	if limit <= 0 {
		limit = defaultAgento11yPageSize
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	return query
}

func (c *Client) listAgento11yEvaluators(ctx context.Context, limit int, cursor string) (*agento11yListResponse[Agento11yEvaluatorDefinition], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yEvaluatorDefinition]](ctx, c, http.MethodGet, "/eval/evaluators", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) getAgento11yEvaluator(ctx context.Context, id string) (*Agento11yEvaluatorDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yEvaluatorDefinition](ctx, c, http.MethodGet, "/eval/evaluators/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) listAgento11yTemplates(ctx context.Context, scope string, limit int, cursor string) (*agento11yListResponse[Agento11yTemplateDefinition], error) {
	query := agento11yPageQuery(limit, cursor)
	if scope != "" {
		query.Set("scope", scope)
	}
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yTemplateDefinition]](ctx, c, http.MethodGet, "/eval/templates", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// getAgento11yTemplate is decoded as map[string]any because the template detail
// nests config, output_keys, and the full version list.
func (c *Client) getAgento11yTemplate(ctx context.Context, id string) (map[string]any, error) {
	return fetchAgento11yJSON[map[string]any](ctx, c, http.MethodGet, "/eval/templates/"+url.PathEscape(id), nil, nil)
}

// listAgento11yTemplateVersions lists the versions of a template. The limit and
// cursor are sent for consistency with the other list routes, but the upstream
// versions handler currently returns every version in one {items} envelope and
// ignores them.
func (c *Client) listAgento11yTemplateVersions(ctx context.Context, id string, limit int, cursor string) (*agento11yListResponse[Agento11yTemplateVersion], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yTemplateVersion]](ctx, c, http.MethodGet, "/eval/templates/"+url.PathEscape(id)+"/versions", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) listAgento11yJudgeProviders(ctx context.Context) (*Agento11yJudgeProvidersResponse, error) {
	resp, err := fetchAgento11yJSON[Agento11yJudgeProvidersResponse](ctx, c, http.MethodGet, "/eval/judge/providers", nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) listAgento11yJudgeModels(ctx context.Context, provider string) (*Agento11yJudgeModelsResponse, error) {
	query := url.Values{}
	if provider != "" {
		query.Set("provider", provider)
	}
	resp, err := fetchAgento11yJSON[Agento11yJudgeModelsResponse](ctx, c, http.MethodGet, "/eval/judge/models", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yEvaluatorRead runs the evaluator read operations shared by the read
// and read-write variants of agento11y_manage_evaluators. Operations it does not
// handle return errAgento11yUnknownOperation.
func (c *Client) agento11yEvaluatorRead(ctx context.Context, operation string, r agento11yEvaluatorReadRequest) (any, error) {
	switch operation {
	case "list_evaluators":
		return c.listAgento11yEvaluators(ctx, r.Limit, r.Cursor)
	case "get_evaluator":
		return c.getAgento11yEvaluator(ctx, r.EvaluatorID)
	case "list_templates":
		return c.listAgento11yTemplates(ctx, r.Scope, r.Limit, r.Cursor)
	case "get_template":
		return c.getAgento11yTemplate(ctx, r.TemplateID)
	case "list_template_versions":
		return c.listAgento11yTemplateVersions(ctx, r.TemplateID, r.Limit, r.Cursor)
	case "list_judge_providers":
		return c.listAgento11yJudgeProviders(ctx)
	case "list_judge_models":
		return c.listAgento11yJudgeModels(ctx, r.Provider)
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yEvaluatorsRead(ctx context.Context, args ManageAgento11yEvaluatorsReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_evaluators: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yEvaluatorRead(ctx, args.Operation, args.readRequest())
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_evaluators: unknown operation %q", args.Operation)
	}
	return result, err
}

// upsertAgento11yEvaluator creates or updates an evaluator. POST is
// create-or-update keyed on definition.evaluator_id; the API has no PUT or
// PATCH route and never takes the evaluator ID in the URL.
func (c *Client) upsertAgento11yEvaluator(ctx context.Context, definition map[string]any) (*Agento11yEvaluatorDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yEvaluatorDefinition](ctx, c, http.MethodPost, "/eval/evaluators", nil, definition)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteAgento11yEvaluator soft-deletes an evaluator. The API answers 204 with
// an empty body.
func (c *Client) deleteAgento11yEvaluator(ctx context.Context, id string) error {
	_, err := c.fetchAgento11y(ctx, http.MethodDelete, "/eval/evaluators/"+url.PathEscape(id), nil, nil)
	return err
}

// forkAgento11yTemplate derives a new evaluator from a template. The ":fork"
// action suffix is appended after escaping the template ID, so it stays a
// literal colon in the path.
func (c *Client) forkAgento11yTemplate(ctx context.Context, templateID string, definition map[string]any) (*Agento11yEvaluatorDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yEvaluatorDefinition](ctx, c, http.MethodPost, "/eval/templates/"+url.PathEscape(templateID)+":fork", nil, definition)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// testAgento11yEvaluator scores one generation with an inline evaluator
// definition and persists nothing. The route sits at the resources root
// (/eval:test), not under /eval/.
func (c *Client) testAgento11yEvaluator(ctx context.Context, definition map[string]any, generationID string) (*Agento11yEvalTestResponse, error) {
	body := make(map[string]any, len(definition)+1)
	for key, value := range definition {
		body[key] = value
	}
	body["generation_id"] = generationID

	resp, err := fetchAgento11yJSON[Agento11yEvalTestResponse](ctx, c, http.MethodPost, "/eval:test", nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yEvaluatorWrite runs the write operations of
// agento11y_manage_evaluators. Operations it does not handle return
// errAgento11yUnknownOperation.
func (c *Client) agento11yEvaluatorWrite(ctx context.Context, operation string, p ManageAgento11yEvaluatorsReadWriteParams) (any, error) {
	switch operation {
	case "upsert_evaluator":
		return c.upsertAgento11yEvaluator(ctx, p.Definition)
	case "delete_evaluator":
		if err := c.deleteAgento11yEvaluator(ctx, p.EvaluatorID); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Evaluator %s deleted successfully", p.EvaluatorID), nil
	case "fork_template":
		return c.forkAgento11yTemplate(ctx, p.TemplateID, p.Definition)
	case "test_evaluator":
		return c.testAgento11yEvaluator(ctx, p.Definition, p.GenerationID)
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yEvaluatorsReadWrite(ctx context.Context, args ManageAgento11yEvaluatorsReadWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_evaluators: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yEvaluatorRead(ctx, args.Operation, args.readRequest())
	if !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}

	result, err = client.agento11yEvaluatorWrite(ctx, args.Operation, args)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_evaluators: unknown operation %q", args.Operation)
	}
	return result, err
}

func (c *Client) listAgento11yEvalRules(ctx context.Context, limit int, cursor string) (*agento11yListResponse[Agento11yRuleDefinition], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yRuleDefinition]](ctx, c, http.MethodGet, "/eval/rules", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) getAgento11yEvalRule(ctx context.Context, id string) (*Agento11yRuleDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yRuleDefinition](ctx, c, http.MethodGet, "/eval/rules/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// listAgento11yGuards lists guards, which the API stores as hook rules under
// /eval/hook-rules; there is no /eval/guards route.
func (c *Client) listAgento11yGuards(ctx context.Context, limit int, cursor string) (*agento11yListResponse[Agento11yHookRuleDefinition], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yHookRuleDefinition]](ctx, c, http.MethodGet, "/eval/hook-rules", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) getAgento11yGuard(ctx context.Context, id string) (*Agento11yHookRuleDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yHookRuleDefinition](ctx, c, http.MethodGet, "/eval/hook-rules/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yEvalRuleRead runs the rule and guard read operations shared by the
// read and read-write variants of agento11y_manage_eval_rules. Operations it
// does not handle return errAgento11yUnknownOperation.
func (c *Client) agento11yEvalRuleRead(ctx context.Context, operation string, r agento11yEvalRuleReadRequest) (any, error) {
	switch operation {
	case "list_rules":
		return c.listAgento11yEvalRules(ctx, r.Limit, r.Cursor)
	case "get_rule":
		return c.getAgento11yEvalRule(ctx, r.RuleID)
	case "list_guards":
		return c.listAgento11yGuards(ctx, r.Limit, r.Cursor)
	case "get_guard":
		return c.getAgento11yGuard(ctx, r.RuleID)
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yEvalRulesRead(ctx context.Context, args ManageAgento11yEvalRulesReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_eval_rules: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yEvalRuleRead(ctx, args.Operation, args.readRequest())
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_eval_rules: unknown operation %q", args.Operation)
	}
	return result, err
}

func (c *Client) createAgento11yEvalRule(ctx context.Context, definition map[string]any) (*Agento11yRuleDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yRuleDefinition](ctx, c, http.MethodPost, "/eval/rules", nil, definition)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateAgento11yEvalRule patches a rule. The rule ID comes from the URL, so it
// must not also appear in the body: the server decodes the PATCH body with
// DisallowUnknownFields and its update DTO has no rule_id field, so a body
// carrying rule_id fails with `unknown field "rule_id"`.
func (c *Client) updateAgento11yEvalRule(ctx context.Context, id string, definition map[string]any) (*Agento11yRuleDefinition, error) {
	body := make(map[string]any, len(definition))
	for key, value := range definition {
		if key == "rule_id" {
			continue
		}
		body[key] = value
	}

	resp, err := fetchAgento11yJSON[Agento11yRuleDefinition](ctx, c, http.MethodPatch, "/eval/rules/"+url.PathEscape(id), nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) deleteAgento11yEvalRule(ctx context.Context, id string) error {
	_, err := c.fetchAgento11y(ctx, http.MethodDelete, "/eval/rules/"+url.PathEscape(id), nil, nil)
	return err
}

// previewAgento11yEvalRule dry-runs a rule against recent traffic. It persists
// nothing but is a POST, so the plugin still requires eval:write.
func (c *Client) previewAgento11yEvalRule(ctx context.Context, definition map[string]any) (*Agento11yRulePreviewResponse, error) {
	resp, err := fetchAgento11yJSON[Agento11yRulePreviewResponse](ctx, c, http.MethodPost, "/eval/rules:preview", nil, definition)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) createAgento11yGuard(ctx context.Context, definition map[string]any) (*Agento11yHookRuleDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yHookRuleDefinition](ctx, c, http.MethodPost, "/eval/hook-rules", nil, definition)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateAgento11yGuard replaces a guard. The hook-rules API has no PATCH, so
// omitted fields reset to server defaults and the caller must send the complete
// state.
func (c *Client) updateAgento11yGuard(ctx context.Context, id string, definition map[string]any) (*Agento11yHookRuleDefinition, error) {
	resp, err := fetchAgento11yJSON[Agento11yHookRuleDefinition](ctx, c, http.MethodPut, "/eval/hook-rules/"+url.PathEscape(id), nil, definition)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) deleteAgento11yGuard(ctx context.Context, id string) error {
	_, err := c.fetchAgento11y(ctx, http.MethodDelete, "/eval/hook-rules/"+url.PathEscape(id), nil, nil)
	return err
}

// agento11yEvalRuleWrite runs the write operations of
// agento11y_manage_eval_rules. Operations it does not handle return
// errAgento11yUnknownOperation.
func (c *Client) agento11yEvalRuleWrite(ctx context.Context, operation string, p ManageAgento11yEvalRulesReadWriteParams) (any, error) {
	switch operation {
	case "create_rule":
		return c.createAgento11yEvalRule(ctx, p.Definition)
	case "update_rule":
		return c.updateAgento11yEvalRule(ctx, p.RuleID, p.Definition)
	case "delete_rule":
		if err := c.deleteAgento11yEvalRule(ctx, p.RuleID); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Eval rule %s deleted successfully", p.RuleID), nil
	case "preview_rule":
		return c.previewAgento11yEvalRule(ctx, p.Definition)
	case "create_guard":
		return c.createAgento11yGuard(ctx, p.Definition)
	case "update_guard":
		return c.updateAgento11yGuard(ctx, p.RuleID, p.Definition)
	case "delete_guard":
		if err := c.deleteAgento11yGuard(ctx, p.RuleID); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Guard %s deleted successfully", p.RuleID), nil
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yEvalRulesReadWrite(ctx context.Context, args ManageAgento11yEvalRulesReadWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_eval_rules: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yEvalRuleRead(ctx, args.Operation, args.readRequest())
	if !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}

	result, err = client.agento11yEvalRuleWrite(ctx, args.Operation, args)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_eval_rules: unknown operation %q", args.Operation)
	}
	return result, err
}
