package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const manageRoutingDescription = `Manage Grafana alerting routing configuration, including notification policies, contact points and time intervals.

Notification policies define how alerts are grouped, routed, and which contact points receive them.
Time intervals define active/mute periods for alert notifications.

When to use:
- Understanding how alerts are routed to contact points/receivers
- Debugging why an alert went to a specific receiver
- Checking grouping, timing, or mute interval settings

When NOT to use:
- Checking alert rule configuration or state (use alerting_rules_read)`

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
	mcpgrafana.WithTitleAnnotation("Manage alerting routing"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

// ManageRoutingWriteParams is the param struct for alerting_routing_write.
type ManageRoutingWriteParams struct {
	Operation             string         `json:"operation" jsonschema:"required,enum=create_contact_point,description=The operation to perform: 'create_contact_point'"`
	Name                  string         `json:"name" jsonschema:"required,description=Contact point name. Integrations with the same name are grouped together."`
	Type                  string         `json:"type" jsonschema:"required,description=Integration type (for example email\\, slack or webhook)."`
	Settings              map[string]any `json:"settings" jsonschema:"required,description=Integration-specific settings as a JSON object (for example {\"addresses\":\"team@example.com\"} for email)."`
	UID                   string         `json:"uid,omitempty" jsonschema:"description=Optional integration UID. Grafana generates one if omitted."`
	DisableResolveMessage bool           `json:"disable_resolve_message,omitempty" jsonschema:"description=Disable resolved notifications. Defaults to false."`
	DisableProvenance     *bool          `json:"disable_provenance,omitempty" jsonschema:"description=Keep the contact point editable in the Grafana UI. Defaults to true."`
}

var contactPointUIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)

func (p ManageRoutingWriteParams) validate() error {
	if p.Operation != "create_contact_point" {
		return fmt.Errorf("unknown operation %q, must be create_contact_point", p.Operation)
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("name is required for 'create_contact_point'")
	}
	if strings.TrimSpace(p.Type) == "" {
		return fmt.Errorf("type is required for 'create_contact_point'")
	}
	if len(p.Settings) == 0 {
		return fmt.Errorf("settings must be a non-empty JSON object")
	}
	if p.UID != "" && !contactPointUIDPattern.MatchString(p.UID) {
		return fmt.Errorf("uid must contain 1 to 40 letters, digits, underscores or hyphens")
	}
	return nil
}

func manageRoutingWrite(ctx context.Context, args ManageRoutingWriteParams) (*contactPointSummary, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("alerting_routing_write: %w", err)
	}
	return createContactPoint(ctx, args)
}

var AlertRoutingWrite = mcpgrafana.MustTool(
	"alerting_routing_write",
	`Create Grafana-managed contact points for alert notifications using operation 'create_contact_point'.
Requires name, type and integration-specific settings. Returns uid, name and type without settings or secrets.
Contact points remain editable in the Grafana UI by default. External Alertmanager receivers are not supported.
Use alerting_manage_routing to list or inspect contact points.`,
	manageRoutingWrite,
	mcpgrafana.WithTitleAnnotation("Write alerting routing"),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)
