package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const manageRoutingDescription = `Manage Grafana alerting routing configuration, including notification policies, contact points and time intervals.

Notification policies define how alerts are grouped, routed, and which contact points receive them.
Time intervals define active/mute periods for alert notifications.

When to use:
- Understanding how alerts are routed to contact points/receivers
- Debugging why an alert went to a specific receiver
- Checking grouping, timing, or mute interval settings

When NOT to use:
- Checking alert rule configuration or state (use alerting_manage_rules)`

// ManageRoutingParams is the param struct for the alerting_manage_routing tool.
type ManageRoutingParams struct {
	Operation         string  `json:"operation" jsonschema:"required,enum=get_notification_policies,enum=get_contact_points,enum=get_contact_point,enum=get_time_intervals,enum=get_time_interval,description=The operation to perform: 'get_notification_policies' to retrieve the notification policy tree\\, 'get_contact_points' to list all contact points\\, 'get_contact_point' to get a specific contact point by name\\, 'get_time_intervals' to list all time intervals\\, 'get_time_interval' to get a specific time interval by name"`
	DatasourceUID     *string `json:"datasource_uid,omitempty" jsonschema:"description=Optional: UID of an Alertmanager-compatible datasource to query for receivers. If omitted\\, returns Grafana-managed contact points. Only used with get_contact_points."`
	Name              *string `json:"name,omitempty" jsonschema:"description=Filter contact points by name (exact match). Only used with get_contact_points."`
	ContactPointTitle *string `json:"contact_point_title,omitempty" jsonschema:"description=Title of the contact point to retrieve (required for get_contact_point operation)"`
	TimeIntervalName  *string `json:"time_interval_name,omitempty" jsonschema:"description=Name of the time interval to retrieve (required for get_time_interval operation)"`
	Limit             int     `json:"limit,omitempty" jsonschema:"description=The maximum number of results to return. Default is 100. Only used with get_contact_points."`
}

func (p ManageRoutingParams) validate() error {
	switch p.Operation {
	case "get_notification_policies":
		return nil
	case "get_contact_points":
		if p.Limit < 0 {
			return fmt.Errorf("invalid limit: %d, must be >= 0", p.Limit)
		}
		return nil
	case "get_contact_point":
		if p.ContactPointTitle == nil || *p.ContactPointTitle == "" {
			return fmt.Errorf("contact_point_title is required for 'get_contact_point' operation")
		}
		return nil
	case "get_time_intervals":
		return nil
	case "get_time_interval":
		if p.TimeIntervalName == nil || *p.TimeIntervalName == "" {
			return fmt.Errorf("time_interval_name is required for 'get_time_interval' operation")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation %q, must be one of: get_notification_policies, get_contact_points, get_contact_point, get_time_intervals, get_time_interval", p.Operation)
	}
}

func (p ManageRoutingParams) toListContactPointsParams() ListContactPointsParams {
	return ListContactPointsParams{
		DatasourceUID: p.DatasourceUID,
		Limit:         p.Limit,
		Name:          p.Name,
	}
}

func manageRouting(ctx context.Context, args ManageRoutingParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_manage_routing: %w", err)
	}

	switch args.Operation {
	case "get_notification_policies":
		return getNotificationPolicies(ctx)
	case "get_contact_points":
		return listContactPoints(ctx, args.toListContactPointsParams())
	case "get_contact_point":
		return getContactPointDetail(ctx, *args.ContactPointTitle)
	case "get_time_intervals":
		return getTimeIntervals(ctx)
	case "get_time_interval":
		return getTimeInterval(ctx, *args.TimeIntervalName)
	}
	return nil, fmt.Errorf("alerting_manage_routing: unknown operation %q", args.Operation)
}

// getNotificationPolicies retrieves the full notification policy tree.
func getNotificationPolicies(ctx context.Context) (*models.Route, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	resp, err := c.Provisioning.GetPolicyTreeWithParams(
		provisioning.NewGetPolicyTreeParamsWithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("get notification policies: %w", err)
	}
	return resp.Payload, nil
}

// getContactPointDetail retrieves full details for a contact point by name,
// including integration settings (unlike the list which returns summaries).
func getContactPointDetail(ctx context.Context, name string) ([]*models.EmbeddedContactPoint, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := provisioning.NewGetContactpointsParams().WithContext(ctx)
	params.Name = &name
	resp, err := c.Provisioning.GetContactpoints(params)
	if err != nil {
		return nil, fmt.Errorf("get contact point %q: %w", name, err)
	}
	if len(resp.Payload) == 0 {
		return nil, fmt.Errorf("contact point %q not found", name)
	}
	return resp.Payload, nil
}

// muteTimingSummary is a compact representation of a mute timing for list output.
type muteTimingSummary struct {
	Name          string                     `json:"name"`
	TimeIntervals []*models.TimeIntervalItem `json:"time_intervals,omitempty"`
}

// getTimeIntervals retrieves all mute timings / time intervals.
func getTimeIntervals(ctx context.Context) ([]muteTimingSummary, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	resp, err := c.Provisioning.GetMuteTimingsWithParams(
		provisioning.NewGetMuteTimingsParamsWithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("get time intervals: %w", err)
	}
	result := make([]muteTimingSummary, 0, len(resp.Payload))
	for _, mt := range resp.Payload {
		result = append(result, muteTimingSummary{
			Name:          mt.Name,
			TimeIntervals: mt.TimeIntervals,
		})
	}
	return result, nil
}

// getTimeInterval retrieves a specific mute timing by name.
func getTimeInterval(ctx context.Context, name string) (*models.MuteTimeInterval, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	resp, err := c.Provisioning.GetMuteTimingWithParams(
		provisioning.NewGetMuteTimingParamsWithContext(ctx).WithName(name),
	)
	if err != nil {
		return nil, fmt.Errorf("get time interval %q: %w", name, err)
	}
	return resp.Payload, nil
}

var ManageRouting = mcpgrafana.MustTool(
	"alerting_manage_routing",
	manageRoutingDescription,
	manageRouting,
	mcp.WithTitleAnnotation("Manage alerting routing"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
