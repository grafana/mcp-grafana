package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/prometheus/alertmanager/api/v2/models"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// ListNotificationGroupsParams is the param struct for the
// alerting_list_notification_groups tool.
type ListNotificationGroupsParams struct {
	Active    *bool    `json:"active,omitempty" jsonschema:"description=Filter for active alerts (omit to include all states)"`
	Silenced  *bool    `json:"silenced,omitempty" jsonschema:"description=Filter for silenced alerts (omit to include all states)"`
	Inhibited *bool    `json:"inhibited,omitempty" jsonschema:"description=Filter for inhibited alerts (omit to include all states)"`
	Filter    []string `json:"filter,omitempty" jsonschema:"description=Label matchers to filter by (e.g. 'severity=critical')"`
	Receiver  string   `json:"receiver,omitempty" jsonschema:"description=Filter by receiver name"`
}

func listNotificationGroups(ctx context.Context, args ListNotificationGroupsParams) ([]*models.AlertGroup, error) {
	client, err := newAlertingClientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerting_list_notification_groups: %w", err)
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
		return nil, fmt.Errorf("alerting_list_notification_groups: %w", err)
	}

	return groups, nil
}

var ListNotificationGroups = mcpgrafana.MustTool(
	"alerting_list_notification_groups",
	"List notification groups from the built-in Grafana Alertmanager. Returns alert groups grouped by alertname and grafana_folder, with per-alert state (active, suppressed, unprocessed), receiver routing, and label set. This is the same data shown on the /alerting/groups page. Supports filtering by active/silenced/inhibited state, label matchers, and receiver name.",
	listNotificationGroups,
	mcp.WithTitleAnnotation("List Alertmanager notification groups"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
