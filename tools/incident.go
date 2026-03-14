package tools

import (
	"context"
	"fmt"

	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ListIncidentsParams struct {
	Limit  int    `json:"limit" jsonschema:"default=10,description=The maximum number of incidents to return"`
	Drill  bool   `json:"drill" jsonschema:"description=Whether to include drill incidents"`
	Status string `json:"status" jsonschema:"description=The status of the incidents to include. Valid values: 'active'\\, 'resolved'"`
}

func listIncidents(ctx context.Context, args ListIncidentsParams) (*incident.QueryIncidentPreviewsResponse, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	is := incident.NewIncidentsService(c)

	// Set default limit to 10 if not specified
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	query := ""
	if !args.Drill {
		query = "isdrill:false"
	}
	if args.Status != "" {
		query += fmt.Sprintf(" status:%s", args.Status)
	}
	incidents, err := is.QueryIncidentPreviews(ctx, incident.QueryIncidentPreviewsRequest{
		Query: incident.IncidentPreviewsQuery{
			QueryString:    query,
			OrderDirection: "DESC",
			Limit:          limit,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	return incidents, nil
}

var ListIncidents = mcpgrafana.MustTool(
	"list_incidents",
	"List Grafana incidents with filtering options to view active or resolved cases. Use when the user wants to review, monitor, or investigate incident status across the Grafana system. Accepts `status` (optional: \"active\" or \"resolved\") and `include_drill` (optional boolean to include drill incidents). e.g., status=\"active\" to show only ongoing incidents. Returns a preview list with basic incident details. Raises an error if the Grafana API is unreachable or authentication fails. Do not use when you need to create a new incident (use create_incident instead).",
	listIncidents,
	mcp.WithTitleAnnotation("List incidents"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type CreateIncidentParams struct {
	Title         string                   `json:"title" jsonschema:"description=The title of the incident"`
	Severity      string                   `json:"severity" jsonschema:"description=The severity of the incident"`
	RoomPrefix    string                   `json:"roomPrefix" jsonschema:"description=The prefix of the room to create the incident in"`
	IsDrill       bool                     `json:"isDrill" jsonschema:"description=Whether the incident is a drill incident"`
	Status        string                   `json:"status" jsonschema:"description=The status of the incident"`
	AttachCaption string                   `json:"attachCaption" jsonschema:"description=The caption of the attachment"`
	AttachURL     string                   `json:"attachUrl" jsonschema:"description=The URL of the attachment"`
	Labels        []incident.IncidentLabel `json:"labels" jsonschema:"description=The labels to add to the incident"`
}

func createIncident(ctx context.Context, args CreateIncidentParams) (*incident.Incident, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	is := incident.NewIncidentsService(c)
	incident, err := is.CreateIncident(ctx, incident.CreateIncidentRequest{
		Title:         args.Title,
		Severity:      args.Severity,
		RoomPrefix:    args.RoomPrefix,
		IsDrill:       args.IsDrill,
		Status:        args.Status,
		AttachCaption: args.AttachCaption,
		AttachURL:     args.AttachURL,
		Labels:        args.Labels,
	})
	if err != nil {
		return nil, fmt.Errorf("create incident: %w", err)
	}
	return &incident.Incident, nil
}

var CreateIncident = mcpgrafana.MustTool(
	"create_incident",
	"Create a new Grafana incident to alert teams about system issues or outages. Use when the user wants to formally escalate a problem that requires immediate attention from on-call engineers or incident response teams. Accepts `title` (required), `severity` (required), `room_prefix` (required for communication channels), `status` (optional), and `labels` (optional for categorization). e.g., title=\"Database Connection Timeout\", severity=\"high\", room_prefix=\"prod-db\". Do not use when you need to create simple notifications or logs (use create_annotation instead). Raises an error if the severity level is invalid or room prefix conflicts with existing channels. This tool triggers alerts and notifications to multiple team members.",
	createIncident,
	mcp.WithTitleAnnotation("Create incident"),
)

type AddActivityToIncidentParams struct {
	IncidentID string `json:"incidentId" jsonschema:"description=The ID of the incident to add the activity to"`
	Body       string `json:"body" jsonschema:"description=The body of the activity. URLs will be parsed and attached as context"`
	EventTime  string `json:"eventTime" jsonschema:"description=The time that the activity occurred. If not provided\\, the current time will be used"`
}

func addActivityToIncident(ctx context.Context, args AddActivityToIncidentParams) (*incident.ActivityItem, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	as := incident.NewActivityService(c)
	activity, err := as.AddActivity(ctx, incident.AddActivityRequest{
		IncidentID:   args.IncidentID,
		ActivityKind: "userNote",
		Body:         args.Body,
		EventTime:    args.EventTime,
	})
	if err != nil {
		return nil, fmt.Errorf("add activity to incident: %w", err)
	}
	return &activity.ActivityItem, nil
}

var AddActivityToIncident = mcpgrafana.MustTool(
	"add_activity_to_incident",
	"Add a note or comment to an existing incident's timeline for documentation and context. Use when the user wants to record observations, updates, or additional information about an ongoing or resolved incident. Accepts `incident_id` (required) and `note_body` (required string that can include URLs for attachments). e.g., incident_id=\"INC-12345\", note_body=\"Root cause identified: database connection timeout\". Do not use when you need to create a new incident or modify incident properties (use appropriate incident management tools instead). Raises an error if the incident ID does not exist or is inaccessible.",
	addActivityToIncident,
	mcp.WithTitleAnnotation("Add activity to incident"),
)

func AddIncidentTools(mcp *server.MCPServer, enableWriteTools bool) {
	ListIncidents.Register(mcp)
	if enableWriteTools {
		CreateIncident.Register(mcp)
		AddActivityToIncident.Register(mcp)
	}
	GetIncident.Register(mcp)
}

type GetIncidentParams struct {
	ID string `json:"id" jsonschema:"description=The ID of the incident to retrieve"`
}

func getIncident(ctx context.Context, args GetIncidentParams) (*incident.Incident, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	is := incident.NewIncidentsService(c)

	incidentResp, err := is.GetIncident(ctx, incident.GetIncidentRequest{
		IncidentID: args.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("get incident by ID: %w", err)
	}

	return &incidentResp.Incident, nil
}

var GetIncident = mcpgrafana.MustTool(
	"get_incident",
	"Retrieve a single incident by its unique identifier and return complete details including title, status, severity, labels, and timestamps. Use when the user wants to examine specific incident information, investigate a particular issue, or get full metadata for one incident. Accepts `incident_id` (required string or number). e.g., incident_id=\"INC-12345\" or incident_id=67890. Returns an error if the incident ID does not exist or access is denied. Do not use when you need to search for multiple incidents or browse incident lists (use list_incidents instead).",
	getIncident,
	mcp.WithTitleAnnotation("Get incident details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
