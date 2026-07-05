package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/prometheus/alertmanager/api/v2/models"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// ListNotificationGroupsParams is the param struct for the
// alertmanager_list_notification_groups tool.
type ListNotificationGroupsParams struct {
	Active    bool     `json:"active,omitempty" jsonschema:"description=Filter for active alerts"`
	Silenced  bool     `json:"silenced,omitempty" jsonschema:"description=Filter for silenced alerts"`
	Inhibited bool     `json:"inhibited,omitempty" jsonschema:"description=Filter for inhibited alerts"`
	Filter    []string `json:"filter,omitempty" jsonschema:"description=Label matchers to filter by (e.g. 'severity=critical')"`
	Receiver  string   `json:"receiver,omitempty" jsonschema:"description=Filter by receiver name"`
}

func listNotificationGroups(ctx context.Context, args ListNotificationGroupsParams) ([]*models.AlertGroup, error) {
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_list_notification_groups: %w", err)
	}

	opts := &GetAlertGroupsOpts{
		Active:    args.Active,
		Silenced:  args.Silenced,
		Inhibited: args.Inhibited,
		Filter:    args.Filter,
		Receiver:  args.Receiver,
	}

	groups, err := client.GetAlertGroups(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_list_notification_groups: %w", err)
	}

	return groups, nil
}

var ListNotificationGroups = mcpgrafana.MustTool(
	"alertmanager_list_notification_groups",
	"List notification groups from the built-in Grafana Alertmanager. Returns alert groups grouped by alertname and grafana_folder, with per-alert state (active, suppressed, unprocessed), receiver routing, and label set. This is the same data shown on the /alerting/groups page. Supports filtering by active/silenced/inhibited state, label matchers, and receiver name.",
	listNotificationGroups,
	mcp.WithTitleAnnotation("List Alertmanager notification groups"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// ListSilencesParams is the param struct for the
// alertmanager_list_silences tool.
type ListSilencesParams struct {
	Filter []string `json:"filter,omitempty" jsonschema:"description=Label matchers to filter by (e.g. 'severity=critical')"`
}

func listSilences(ctx context.Context, args ListSilencesParams) ([]*models.GettableSilence, error) {
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_list_silences: %w", err)
	}

	opts := &GetSilencesOpts{
		Filter: args.Filter,
	}

	silences, err := client.GetSilences(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_list_silences: %w", err)
	}

	return silences, nil
}

var ListSilences = mcpgrafana.MustTool(
	"alertmanager_list_silences",
	"List current silences from the built-in Grafana Alertmanager. Returns silences with matchers, creator, comment, and expiry.",
	listSilences,
	mcp.WithTitleAnnotation("List Alertmanager silences"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// CreateSilenceParams is the param struct for the
// alertmanager_create_silence tool.
type CreateSilenceParams struct {
	Matchers  []string `json:"matchers" jsonschema:"required,description=Label matchers for the silence (e.g. 'alertname=HighCPU'\\, 'severity=critical')"`
	StartsAt  string   `json:"startsAt" jsonschema:"required,description=Start time in ISO 8601 format (e.g. '2024-01-01T00:00:00Z')"`
	EndsAt    string   `json:"endsAt" jsonschema:"required,description=End time in ISO 8601 format (e.g. '2024-01-02T00:00:00Z')"`
	Comment   string   `json:"comment" jsonschema:"required,description=Comment explaining the reason for the silence"`
	CreatedBy string   `json:"createdBy" jsonschema:"required,description=Name or identifier of the creator"`
}

// createSilenceResult contains the result of a successful silence creation.
type createSilenceResult struct {
	SilenceID string `json:"silenceID"`
	Message   string `json:"message"`
}

func (p CreateSilenceParams) validate() error {
	if len(p.Matchers) == 0 {
		return fmt.Errorf("at least one matcher is required")
	}
	if p.StartsAt == "" {
		return fmt.Errorf("startsAt is required")
	}
	if p.EndsAt == "" {
		return fmt.Errorf("endsAt is required")
	}
	if p.Comment == "" {
		return fmt.Errorf("comment is required")
	}
	if p.CreatedBy == "" {
		return fmt.Errorf("createdBy is required")
	}
	return nil
}

func createSilence(ctx context.Context, args CreateSilenceParams) (*createSilenceResult, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alertmanager_create_silence: %w", err)
	}

	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_create_silence: %w", err)
	}

	// Parse matchers
	var matchers models.Matchers
	for _, m := range args.Matchers {
		parts := strings.SplitN(m, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("alertmanager_create_silence: invalid matcher %q, expected format 'key=value'", m)
		}
		isRegex := false
		matchers = append(matchers, &models.Matcher{
			Name:    &parts[0],
			Value:   &parts[1],
			IsRegex: &isRegex,
		})
	}

	// Parse timestamps
	startsAt, err := time.Parse(time.RFC3339, args.StartsAt)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_create_silence: invalid startsAt %q: %w", args.StartsAt, err)
	}
	endsAt, err := time.Parse(time.RFC3339, args.EndsAt)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_create_silence: invalid endsAt %q: %w", args.EndsAt, err)
	}

	startsAtStrfmt := strfmt.DateTime(startsAt)
	endsAtStrfmt := strfmt.DateTime(endsAt)

	silence := &models.PostableSilence{
		Silence: models.Silence{
			Matchers:  matchers,
			StartsAt:  &startsAtStrfmt,
			EndsAt:    &endsAtStrfmt,
			Comment:   &args.Comment,
			CreatedBy: &args.CreatedBy,
		},
	}

	silenceID, err := client.CreateSilence(ctx, silence)
	if err != nil {
		return nil, fmt.Errorf("alertmanager_create_silence: %w", err)
	}

	return &createSilenceResult{
		SilenceID: silenceID,
		Message:   "Silence created successfully",
	}, nil
}

var CreateSilence = mcpgrafana.MustTool(
	"alertmanager_create_silence",
	"Create a new silence in the built-in Grafana Alertmanager. Silences suppress alert notifications for matching alerts during the specified time window.",
	createSilence,
	mcp.WithTitleAnnotation("Create Alertmanager silence"),
	mcp.WithDestructiveHintAnnotation(true),
)
