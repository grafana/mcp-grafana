package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/prometheus/alertmanager/api/v2/models"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const manageSilencesDescription = `Manage Grafana alerting silences. A silence temporarily suppresses notifications for alerts whose labels match a set of matchers, without changing the alert rules themselves.

Operations:
- 'list': list existing silences. Optionally filter by rule_uid (matches the __alert_rule_uid__ label) or by matchers.
- 'get': retrieve a single silence by silence_id.
- 'create': create a new silence. Requires matchers, starts_at, ends_at (RFC3339) and comment.
- 'update': modify an existing silence by silence_id. Requires matchers, starts_at, ends_at and comment. The id is only kept when the posted matchers and starts_at match the stored ones, so pass back the starts_at returned by 'get'; otherwise Alertmanager expires the old silence and returns a new id.
- 'delete': expire/remove a silence by silence_id.

When to use:
- Muting noisy or expected alerts during maintenance windows
- Inspecting or cleaning up existing silences

When NOT to use:
- Changing alert rule configuration or state (use alerting_manage_rules)
- Changing how alerts are routed to receivers (use alerting_manage_routing)`

const manageSilencesReadDescription = `List and inspect Grafana alerting silences. A silence temporarily suppresses notifications for alerts whose labels match a set of matchers.

Operations:
- 'list': list existing silences. Optionally filter by rule_uid (matches the __alert_rule_uid__ label) or by matchers.
- 'get': retrieve a single silence by silence_id.

When to use:
- Inspecting which alerts are currently silenced and why

When NOT to use:
- Creating, updating or deleting silences (read-only tool)
- Changing alert rule configuration or state (use alerting_manage_rules)
- Changing how alerts are routed to receivers (use alerting_manage_routing)`

const (
	// silencesBasePath is the Grafana-managed Alertmanager v2 base path.
	silencesBasePath = "/api/alertmanager/grafana/api/v2"
	// defaultSilenceCreatedBy is used when the caller does not supply created_by.
	defaultSilenceCreatedBy = "grafana-assistant"
	// ruleUIDLabel is the reserved label used to scope a silence to a rule.
	ruleUIDLabel = "__alert_rule_uid__"
)

// SilenceMatcherParam describes a single label matcher for a silence.
type SilenceMatcherParam struct {
	Name    string `json:"name" jsonschema:"required,description=The label name to match"`
	Value   string `json:"value" jsonschema:"required,description=The label value to match against"`
	IsRegex bool   `json:"isRegex,omitempty" jsonschema:"description=Whether value is a regular expression. Defaults to false."`
	IsEqual *bool  `json:"isEqual,omitempty" jsonschema:"description=Whether the matcher asserts equality (true) or inequality (false). Defaults to true."`
}

// ManageSilencesParams is the param struct for the alerting_manage_silences tool.
type ManageSilencesParams struct {
	Operation string                `json:"operation" jsonschema:"required,enum=list,enum=get,enum=create,enum=update,enum=delete,description=The operation to perform: 'list' to list silences\\, 'get' to retrieve a silence by id\\, 'create' to create a new silence\\, 'update' to modify an existing silence by id\\, 'delete' to expire a silence by id"`
	SilenceID *string               `json:"silence_id,omitempty" jsonschema:"description=The silence id (required for 'get'\\, 'update' and 'delete')"`
	RuleUID   *string               `json:"rule_uid,omitempty" jsonschema:"description=Optional: filter listed silences to those scoped to this alert rule UID (matches the __alert_rule_uid__ label). Only used with 'list'."`
	Matchers  []SilenceMatcherParam `json:"matchers,omitempty" jsonschema:"description=Label matchers. Required (at least one) for 'create' and 'update'. For 'list'\\, used as an optional filter."`
	StartsAt  *string               `json:"starts_at,omitempty" jsonschema:"description=Silence start time in RFC3339 format\\, e.g. '2026-07-11T10:00:00Z' (required for 'create' and 'update'). Grafana clamps a start time in the past to the moment of creation\\, so for 'update' pass back the starts_at returned by 'get' to keep the silence id stable."`
	EndsAt    *string               `json:"ends_at,omitempty" jsonschema:"description=Silence end time in RFC3339 format\\, e.g. '2026-07-11T12:00:00Z' (required for 'create' and 'update')"`
	Comment   *string               `json:"comment,omitempty" jsonschema:"description=A human-readable comment explaining the silence (required for 'create' and 'update')"`
	CreatedBy *string               `json:"created_by,omitempty" jsonschema:"description=Author of the silence. Defaults to 'grafana-assistant'."`
}

func (p ManageSilencesParams) validate() error {
	switch p.Operation {
	case "list":
		return nil
	case "get":
		return requireSilenceID(p.SilenceID, "get")
	case "create":
		// A POST that carries an id is treated as an update by the
		// Alertmanager API, so an accidental silence_id here could
		// silently overwrite an existing silence.
		if p.SilenceID != nil && *p.SilenceID != "" {
			return fmt.Errorf("silence_id must not be set for 'create' (use 'update' to modify an existing silence)")
		}
		return p.validateWritePayload()
	case "update":
		if err := requireSilenceID(p.SilenceID, "update"); err != nil {
			return err
		}
		return p.validateWritePayload()
	case "delete":
		return requireSilenceID(p.SilenceID, "delete")
	default:
		return fmt.Errorf("unknown operation %q, must be one of: list, get, create, update, delete", p.Operation)
	}
}

// ManageSilencesReadParams is the param struct for the read-only variant of
// alerting_manage_silences (list/get only).
type ManageSilencesReadParams struct {
	Operation string                `json:"operation" jsonschema:"required,enum=list,enum=get,description=The operation to perform: 'list' to list silences\\, 'get' to retrieve a silence by id"`
	SilenceID *string               `json:"silence_id,omitempty" jsonschema:"description=The silence id (required for 'get')"`
	RuleUID   *string               `json:"rule_uid,omitempty" jsonschema:"description=Optional: filter listed silences to those scoped to this alert rule UID (matches the __alert_rule_uid__ label). Only used with 'list'."`
	Matchers  []SilenceMatcherParam `json:"matchers,omitempty" jsonschema:"description=Optional label matchers used to filter listed silences. Only used with 'list'."`
}

func (p ManageSilencesReadParams) validate() error {
	switch p.Operation {
	case "list":
		return nil
	case "get":
		return requireSilenceID(p.SilenceID, "get")
	default:
		return fmt.Errorf("unknown operation %q, must be one of: list, get", p.Operation)
	}
}

func requireSilenceID(id *string, operation string) error {
	if id == nil || *id == "" {
		return fmt.Errorf("silence_id is required for '%s' operation", operation)
	}
	return nil
}

// validateWritePayload checks the fields shared by 'create' and 'update'.
func (p ManageSilencesParams) validateWritePayload() error {
	if len(p.Matchers) == 0 {
		return fmt.Errorf("matchers is required and must contain at least one matcher")
	}
	for i, m := range p.Matchers {
		if m.Name == "" {
			return fmt.Errorf("matcher at index %d: name is required", i)
		}
	}
	startsAt, err := parseRFC3339("starts_at", p.StartsAt)
	if err != nil {
		return err
	}
	endsAt, err := parseRFC3339("ends_at", p.EndsAt)
	if err != nil {
		return err
	}
	// Grafana rejects this server-side with a generic 400, so catching it here
	// costs a round-trip less and names the offending fields.
	if !endsAt.After(startsAt) {
		return fmt.Errorf("ends_at (%s) must be after starts_at (%s)", *p.EndsAt, *p.StartsAt)
	}
	// A window that has already closed silences nothing. Expiring a silence
	// early is what 'delete' is for, so it is not a reason to allow it here.
	if !endsAt.After(time.Now()) {
		if p.Operation == "update" {
			return fmt.Errorf("ends_at (%s) is in the past; use the 'delete' operation to expire a silence early", *p.EndsAt)
		}
		return fmt.Errorf("ends_at (%s) is in the past, so the silence would be expired on creation", *p.EndsAt)
	}
	if p.Comment == nil || *p.Comment == "" {
		return fmt.Errorf("comment is required for create/update")
	}
	return nil
}

func parseRFC3339(field string, v *string) (time.Time, error) {
	if v == nil || *v == "" {
		return time.Time{}, fmt.Errorf("%s is required for create/update", field)
	}
	t, err := time.Parse(time.RFC3339, *v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be a valid RFC3339 timestamp: %w", field, err)
	}
	return t, nil
}

// buildSilenceFilters converts an optional rule UID and matcher list into the
// repeated `filter` query values accepted by the Alertmanager silences API.
func buildSilenceFilters(ruleUID *string, matchers []SilenceMatcherParam) []string {
	var filters []string
	if ruleUID != nil && *ruleUID != "" {
		filters = append(filters, matcherToFilterString(SilenceMatcherParam{
			Name:  ruleUIDLabel,
			Value: *ruleUID,
		}))
	}
	for _, m := range matchers {
		filters = append(filters, matcherToFilterString(m))
	}
	return filters
}

// matcherToFilterString renders a matcher as an Alertmanager filter expression,
// e.g. `severity="critical"`, `pod=~"api-.*"`, `env!="prod"`.
func matcherToFilterString(m SilenceMatcherParam) string {
	isEqual := m.IsEqual == nil || *m.IsEqual
	var op string
	switch {
	case m.IsRegex && isEqual:
		op = "=~"
	case m.IsRegex && !isEqual:
		op = "!~"
	case !m.IsRegex && !isEqual:
		op = "!="
	default:
		op = "="
	}
	return fmt.Sprintf("%s%s%q", m.Name, op, m.Value)
}

// toPostableSilence builds the Alertmanager v2 request body for create/update,
// applying the created_by default and carrying the silence id through for
// updates. SilenceMatcherParam stays the tool-facing input shape (plain fields
// so the generated JSON schema is readable); models.Matcher is what goes on the
// wire.
func (p ManageSilencesParams) toPostableSilence() (models.PostableSilence, error) {
	startsAt, err := parseRFC3339("starts_at", p.StartsAt)
	if err != nil {
		return models.PostableSilence{}, err
	}
	endsAt, err := parseRFC3339("ends_at", p.EndsAt)
	if err != nil {
		return models.PostableSilence{}, err
	}

	createdBy := defaultSilenceCreatedBy
	if p.CreatedBy != nil && *p.CreatedBy != "" {
		createdBy = *p.CreatedBy
	}
	comment := derefSilenceStr(p.Comment)

	matchers := make(models.Matchers, 0, len(p.Matchers))
	for _, m := range p.Matchers {
		name, value, isRegex := m.Name, m.Value, m.IsRegex
		matchers = append(matchers, &models.Matcher{
			Name:    &name,
			Value:   &value,
			IsRegex: &isRegex,
			IsEqual: m.IsEqual,
		})
	}

	start, end := strfmt.DateTime(startsAt), strfmt.DateTime(endsAt)
	s := models.PostableSilence{
		Silence: models.Silence{
			Matchers:  matchers,
			StartsAt:  &start,
			EndsAt:    &end,
			Comment:   &comment,
			CreatedBy: &createdBy,
		},
	}
	if p.SilenceID != nil {
		s.ID = *p.SilenceID
	}
	return s, nil
}

func derefSilenceStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// manageSilencesRead backs the read-only tool variant (list/get only).
func manageSilencesRead(ctx context.Context, args ManageSilencesReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_manage_silences: %w", err)
	}

	c, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerting_manage_silences: %w", err)
	}

	switch args.Operation {
	case "list":
		return c.listSilences(ctx, buildSilenceFilters(args.RuleUID, args.Matchers))
	case "get":
		return c.getSilence(ctx, *args.SilenceID)
	}
	return nil, fmt.Errorf("alerting_manage_silences: unknown operation %q", args.Operation)
}

func manageSilencesReadWrite(ctx context.Context, args ManageSilencesParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_manage_silences: %w", err)
	}

	c, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerting_manage_silences: %w", err)
	}

	switch args.Operation {
	case "list":
		return c.listSilences(ctx, buildSilenceFilters(args.RuleUID, args.Matchers))
	case "get":
		return c.getSilence(ctx, *args.SilenceID)
	case "create", "update":
		s, err := args.toPostableSilence()
		if err != nil {
			return nil, fmt.Errorf("alerting_manage_silences: %w", err)
		}
		return c.createOrUpdateSilence(ctx, s)
	case "delete":
		return c.deleteSilence(ctx, *args.SilenceID)
	}
	return nil, fmt.Errorf("alerting_manage_silences: unknown operation %q", args.Operation)
}

// silenceRequest performs a JSON HTTP request against the Alertmanager silences
// API. Unlike makeRequest it accepts any method + body and treats any 2xx as
// success, decoding the response only when a body is present (DELETE returns 200
// with an empty body).
func (c *alertingClient) silenceRequest(ctx context.Context, method, path string, params url.Values, body, out any) error {
	u := c.baseURL.JoinPath(path)
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	p := u.String()

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, p, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request to %s: %w", p, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request to %s: %w", p, err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck
	}()

	bodyBytes, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		// Keep the status code even when the body is unreadable or oversized:
		// it is usually the only actionable part of a failed request.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("grafana API returned status code %d and the response body could not be read: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("failed to read response body from %s: %w", p, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("grafana API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	if out == nil || len(bytes.TrimSpace(bodyBytes)) == 0 {
		return nil
	}
	if err := json.Unmarshal(bodyBytes, out); err != nil {
		return fmt.Errorf("failed to decode response from %s: %w", p, err)
	}
	return nil
}

func (c *alertingClient) listSilences(ctx context.Context, filters []string) (models.GettableSilences, error) {
	params := url.Values{}
	for _, f := range filters {
		params.Add("filter", f)
	}
	var out models.GettableSilences
	if err := c.silenceRequest(ctx, http.MethodGet, silencesBasePath+"/silences", params, nil, &out); err != nil {
		return nil, fmt.Errorf("failed to list silences: %w", err)
	}
	return out, nil
}

func (c *alertingClient) getSilence(ctx context.Context, id string) (*models.GettableSilence, error) {
	var out models.GettableSilence
	path := silencesBasePath + "/silence/" + url.PathEscape(id)
	if err := c.silenceRequest(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, fmt.Errorf("failed to get silence %q: %w", id, err)
	}
	return &out, nil
}

// createSilenceResponse is the body returned by POST /silences. The generated
// equivalent (models.PostSilencesOKBody) lives in the alertmanager restapi
// operations package, which would drag in the go-openapi runtime for a
// single-field envelope, so it stays hand-rolled here.
type createSilenceResponse struct {
	SilenceID string `json:"silenceID"`
}

func (c *alertingClient) createOrUpdateSilence(ctx context.Context, s models.PostableSilence) (*createSilenceResponse, error) {
	var out createSilenceResponse
	if err := c.silenceRequest(ctx, http.MethodPost, silencesBasePath+"/silences", nil, s, &out); err != nil {
		return nil, fmt.Errorf("failed to create/update silence: %w", err)
	}
	return &out, nil
}

func (c *alertingClient) deleteSilence(ctx context.Context, id string) (any, error) {
	path := silencesBasePath + "/silence/" + url.PathEscape(id)
	if err := c.silenceRequest(ctx, http.MethodDelete, path, nil, nil, nil); err != nil {
		return nil, fmt.Errorf("failed to delete silence %q: %w", id, err)
	}
	return map[string]string{"status": "deleted", "silence_id": id}, nil
}

// ManageSilencesRead is the read-only variant (list/get). It shares the tool
// name with ManageSilencesReadWrite so the agent-side allow-list does not fork;
// exactly one of the two is registered depending on whether write tools are
// enabled.
var ManageSilencesRead = mcpgrafana.MustTool(
	"alerting_manage_silences",
	manageSilencesReadDescription,
	manageSilencesRead,
	mcp.WithTitleAnnotation("Manage alerting silences"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

// ManageSilencesReadWrite is the write-capable variant (create/update/delete
// silences), so it is not marked read-only.
var ManageSilencesReadWrite = mcpgrafana.MustTool(
	"alerting_manage_silences",
	manageSilencesDescription,
	manageSilencesReadWrite,
	mcp.WithTitleAnnotation("Manage alerting silences"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)
