package tools

import (
	"context"
	"fmt"
	"time"

	irmclient "github.com/grafana/gcx/client/irm"
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

type incidentPreviewSummary struct {
	IncidentID    string `json:"incidentId"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Severity      string `json:"severity"`
	CreatedTime   string `json:"createdTime,omitempty"`
	ModifiedTime  string `json:"modifiedTime,omitempty"`
	IncidentStart string `json:"incidentStart,omitempty"`
	IsDrill       bool   `json:"isDrill,omitempty"`
}

type ListIncidentsResult struct {
	Incidents []incidentPreviewSummary `json:"incidents"`
	HasMore   bool                     `json:"hasMore"`
}

func formatIncidentTime(t irmclient.FlexTime) string {
	ts := time.Time(t)
	if ts.IsZero() {
		return ""
	}
	return ts.Format(time.RFC3339)
}

func summarizeIncidents(incidents []irmclient.Incident) []incidentPreviewSummary {
	result := make([]incidentPreviewSummary, 0, len(incidents))
	for _, i := range incidents {
		result = append(result, incidentPreviewSummary{
			IncidentID:    i.IncidentID,
			Title:         i.Title,
			Status:        i.Status,
			Severity:      i.Severity,
			CreatedTime:   formatIncidentTime(i.CreatedTime),
			ModifiedTime:  formatIncidentTime(i.ModifiedTime),
			IncidentStart: formatIncidentTime(i.IncidentStart),
			IsDrill:       i.IsDrill,
		})
	}
	return result
}

func listIncidents(ctx context.Context, args ListIncidentsParams) (*ListIncidentsResult, error) {
	c := mcpgrafana.IRMClientFromContext(ctx)
	if c == nil {
		return nil, fmt.Errorf("list incidents: no IRM client configured")
	}

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

	// client/irm.List surfaces no response cursor, so hasMore is derived by
	// asking for one incident more than the caller wants.
	incidents, err := c.List(ctx, irmclient.IncidentQuery{
		QueryString:    query,
		OrderDirection: "DESC",
		Limit:          limit + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}

	hasMore := len(incidents) > limit
	if hasMore {
		incidents = incidents[:limit]
	}

	return &ListIncidentsResult{
		Incidents: summarizeIncidents(incidents),
		HasMore:   hasMore,
	}, nil
}

var ListIncidents = mcpgrafana.MustTool(
	"list_incidents",
	"List Grafana incidents. Allows filtering by status ('active', 'resolved') and optionally including drill incidents. Returns a preview list with basic details.",
	listIncidents,
	mcp.WithTitleAnnotation("List incidents"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type CreateIncidentParams struct {
	Title         string                   `json:"title" jsonschema:"required,description=The title of the incident"`
	Severity      string                   `json:"severity" jsonschema:"required,description=The severity of the incident"`
	RoomPrefix    string                   `json:"roomPrefix" jsonschema:"required,description=The prefix of the room to create the incident in"`
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
	"Create a new Grafana incident. Requires title, severity, and room prefix. Allows setting status and labels. This tool should be used judiciously and sparingly, and only after confirmation from the user, as it may notify or alarm lots of people.",
	createIncident,
	mcp.WithTitleAnnotation("Create incident"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type AddActivityToIncidentParams struct {
	IncidentID string `json:"incidentId" jsonschema:"required,description=The ID of the incident to add the activity to"`
	Body       string `json:"body" jsonschema:"required,description=The body of the activity. URLs will be parsed and attached as context"`
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
	"Add a note (userNote activity) to an existing incident's timeline using its ID. The note body can include URLs which will be attached as context. Use this to add context to an incident.",
	addActivityToIncident,
	mcp.WithTitleAnnotation("Add activity to incident"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
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
	ID string `json:"id" jsonschema:"required,description=The ID of the incident to retrieve"`
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
	"Get a single incident by ID. Returns the full incident details including title, status, severity, labels, timestamps, and other metadata.",
	getIncident,
	mcp.WithTitleAnnotation("Get incident details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)
