package tools

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/mark3labs/mcp-go/mcp"
	ammodels "github.com/prometheus/alertmanager/api/v2/models"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const manageAlertmanagerDescriptionFmt = `%s

This tool operates on an external Alertmanager datasource through Grafana's datasource proxy.

When to use:
- Listing active Alertmanager alerts/notifications received by Alertmanager
- Listing or inspecting silences in an external Alertmanager
%s
When NOT to use:
- Managing Grafana-managed notification policies or contact points (use alerting_manage_routing)
- Inspecting Grafana alert rule definitions (use alerting_manage_rules)`

func manageAlertmanagerDescription(readOnly bool) string {
	if readOnly {
		return fmt.Sprintf(manageAlertmanagerDescriptionFmt,
			"List active alerts and inspect silences in an external Alertmanager datasource.",
			"",
		)
	}
	return fmt.Sprintf(manageAlertmanagerDescriptionFmt,
		"List active alerts and manage silences in an external Alertmanager datasource.",
		"- Creating, updating, or deleting Alertmanager silences\n",
	)
}

type AlertmanagerMatcherParam struct {
	Name    string `json:"name" jsonschema:"required,description=Matcher label name."`
	Value   string `json:"value" jsonschema:"required,description=Matcher label value."`
	IsRegex bool   `json:"is_regex,omitempty" jsonschema:"description=Whether the value is a regular expression."`
	IsEqual *bool  `json:"is_equal,omitempty" jsonschema:"description=Whether the matcher is equality-based. Defaults to true; set false for negative matchers."`
}

type ManageAlertmanagerParams struct {
	Operation     string                     `json:"operation" jsonschema:"required,enum=list_alerts,enum=list_silences,enum=get_silence,enum=create_or_update_silence,enum=delete_silence,description=Operation to perform. Read-only mode supports list_alerts\, list_silences\, and get_silence. Write mode also supports create_or_update_silence and delete_silence."`
	DatasourceUID string                     `json:"datasource_uid" jsonschema:"required,description=UID of an Alertmanager-compatible datasource."`
	SilenceID     string                     `json:"silence_id,omitempty" jsonschema:"description=Silence ID. Required for get_silence and delete_silence; optional for create_or_update_silence to update an existing silence."`
	Matchers      []AlertmanagerMatcherParam `json:"matchers,omitempty" jsonschema:"description=Matchers for list filters or silence creation. For list_alerts/list_silences they are encoded as Alertmanager filter query parameters. Required for create_or_update_silence."`
	StartsAt      string                     `json:"starts_at,omitempty" jsonschema:"description=Silence start time in RFC3339 format. Required for create_or_update_silence."`
	EndsAt        string                     `json:"ends_at,omitempty" jsonschema:"description=Silence end time in RFC3339 format. Required for create_or_update_silence."`
	CreatedBy     string                     `json:"created_by,omitempty" jsonschema:"description=Silence creator. Required for create_or_update_silence."`
	Comment       string                     `json:"comment,omitempty" jsonschema:"description=Silence comment. Required for create_or_update_silence."`
	Active        *bool                      `json:"active,omitempty" jsonschema:"description=Filter list_alerts by active alerts. Defaults to true."`
	Silenced      *bool                      `json:"silenced,omitempty" jsonschema:"description=Filter list_alerts by silenced alerts."`
	Inhibited     *bool                      `json:"inhibited,omitempty" jsonschema:"description=Filter list_alerts by inhibited alerts."`
	Unprocessed   *bool                      `json:"unprocessed,omitempty" jsonschema:"description=Filter list_alerts by unprocessed alerts."`
}

func (p ManageAlertmanagerParams) validate(readOnly bool) error {
	if p.DatasourceUID == "" {
		return fmt.Errorf("datasource_uid is required")
	}
	switch p.Operation {
	case "list_alerts", "list_silences":
		return nil
	case "get_silence":
		if p.SilenceID == "" {
			return fmt.Errorf("silence_id is required for 'get_silence' operation")
		}
		return nil
	case "create_or_update_silence":
		if readOnly {
			return fmt.Errorf("operation %q requires write tools to be enabled", p.Operation)
		}
		if len(p.Matchers) == 0 {
			return fmt.Errorf("matchers are required for 'create_or_update_silence' operation")
		}
		if p.StartsAt == "" || p.EndsAt == "" || p.CreatedBy == "" || p.Comment == "" {
			return fmt.Errorf("starts_at, ends_at, created_by, and comment are required for 'create_or_update_silence' operation")
		}
		return nil
	case "delete_silence":
		if readOnly {
			return fmt.Errorf("operation %q requires write tools to be enabled", p.Operation)
		}
		if p.SilenceID == "" {
			return fmt.Errorf("silence_id is required for 'delete_silence' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: list_alerts, list_silences, get_silence, create_or_update_silence, delete_silence", p.Operation)
	}
}

func manageAlertmanagerRead(ctx context.Context, args ManageAlertmanagerParams) (any, error) {
	return manageAlertmanager(ctx, args, true)
}

func manageAlertmanagerReadWrite(ctx context.Context, args ManageAlertmanagerParams) (any, error) {
	return manageAlertmanager(ctx, args, false)
}

func manageAlertmanager(ctx context.Context, args ManageAlertmanagerParams, readOnly bool) (any, error) {
	if err := args.validate(readOnly); err != nil {
		return nil, fmt.Errorf("alerting_manage_alertmanager: %w", err)
	}
	if err := validateAlertmanagerDatasource(ctx, args.DatasourceUID); err != nil {
		return nil, fmt.Errorf("alerting_manage_alertmanager: %w", err)
	}

	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating alerting client: %w", err)
	}

	switch args.Operation {
	case "list_alerts":
		return client.GetAlertmanagerAlerts(ctx, args.DatasourceUID, args.alertsQueryValues())
	case "list_silences":
		return client.GetAlertmanagerSilences(ctx, args.DatasourceUID, args.silencesQueryValues())
	case "get_silence":
		return client.GetAlertmanagerSilence(ctx, args.DatasourceUID, args.SilenceID)
	case "create_or_update_silence":
		silence, err := args.toPostableSilence()
		if err != nil {
			return nil, fmt.Errorf("build silence: %w", err)
		}
		return client.CreateOrUpdateAlertmanagerSilence(ctx, args.DatasourceUID, silence)
	case "delete_silence":
		return client.DeleteAlertmanagerSilence(ctx, args.DatasourceUID, args.SilenceID)
	}
	return nil, fmt.Errorf("alerting_manage_alertmanager: unknown operation %q", args.Operation)
}

func validateAlertmanagerDatasource(ctx context.Context, datasourceUID string) error {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: datasourceUID})
	if err != nil {
		return fmt.Errorf("datasource %s: %w", datasourceUID, err)
	}
	if !isAlertmanagerDatasource(ds.Type) {
		return fmt.Errorf("datasource %s (type: %s) is not an Alertmanager datasource", datasourceUID, ds.Type)
	}
	return nil
}

func (p ManageAlertmanagerParams) alertsQueryValues() url.Values {
	params := p.matcherQueryValues()
	setBoolQuery(params, "active", p.Active, true)
	setBoolQuery(params, "silenced", p.Silenced, false)
	setBoolQuery(params, "inhibited", p.Inhibited, false)
	setBoolQuery(params, "unprocessed", p.Unprocessed, false)
	return params
}

func (p ManageAlertmanagerParams) silencesQueryValues() url.Values {
	return p.matcherQueryValues()
}

func (p ManageAlertmanagerParams) matcherQueryValues() url.Values {
	params := url.Values{}
	for _, m := range p.Matchers {
		params.Add("filter", formatAlertmanagerMatcher(m))
	}
	return params
}

func setBoolQuery(params url.Values, name string, value *bool, defaultValue bool) {
	if value == nil {
		params.Set(name, fmt.Sprintf("%t", defaultValue))
		return
	}
	params.Set(name, fmt.Sprintf("%t", *value))
}

func formatAlertmanagerMatcher(m AlertmanagerMatcherParam) string {
	isEqual := true
	if m.IsEqual != nil {
		isEqual = *m.IsEqual
	}
	operator := "="
	if m.IsRegex && isEqual {
		operator = "=~"
	} else if m.IsRegex && !isEqual {
		operator = "!~"
	} else if !isEqual {
		operator = "!="
	}
	return fmt.Sprintf("%s%s\"%s\"", m.Name, operator, m.Value)
}

func (p ManageAlertmanagerParams) toPostableSilence() (*ammodels.PostableSilence, error) {
	startsAt, err := time.Parse(time.RFC3339, p.StartsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid starts_at %q: %w", p.StartsAt, err)
	}
	endsAt, err := time.Parse(time.RFC3339, p.EndsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid ends_at %q: %w", p.EndsAt, err)
	}
	if !endsAt.After(startsAt) {
		return nil, fmt.Errorf("ends_at must be after starts_at")
	}

	matchers := make(ammodels.Matchers, 0, len(p.Matchers))
	for _, matcher := range p.Matchers {
		isEqual := true
		if matcher.IsEqual != nil {
			isEqual = *matcher.IsEqual
		}
		isRegex := matcher.IsRegex
		name := matcher.Name
		value := matcher.Value
		matchers = append(matchers, &ammodels.Matcher{
			IsEqual: &isEqual,
			IsRegex: &isRegex,
			Name:    &name,
			Value:   &value,
		})
	}

	start := strfmt.DateTime(startsAt)
	end := strfmt.DateTime(endsAt)
	return &ammodels.PostableSilence{
		ID: p.SilenceID,
		Silence: ammodels.Silence{
			Comment:   &p.Comment,
			CreatedBy: &p.CreatedBy,
			EndsAt:    &end,
			Matchers:  matchers,
			StartsAt:  &start,
		},
	}, nil
}

var ManageAlertmanagerRead = mcpgrafana.MustTool(
	"alerting_manage_alertmanager",
	manageAlertmanagerDescription(true),
	manageAlertmanagerRead,
	mcp.WithTitleAnnotation("Manage external Alertmanager"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var ManageAlertmanagerReadWrite = mcpgrafana.MustTool(
	"alerting_manage_alertmanager",
	manageAlertmanagerDescription(false),
	manageAlertmanagerReadWrite,
	mcp.WithTitleAnnotation("Manage external Alertmanager"),
	mcp.WithDestructiveHintAnnotation(true),
)
