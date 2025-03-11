package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ListIncidentsParams struct {
	Limit  int    `json:"limit" jsonschema:"description=The maximum number of incidents to return"`
	Drill  bool   `json:"drill" jsonschema:"description=Whether to include drill incidents"`
	Status string `json:"status" jsonschema:"description=The status of the incidents to include"`
}

func listIncidents(ctx context.Context, args ListIncidentsParams) (*incident.QueryIncidentsResponse, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	is := incident.NewIncidentsService(c)
	query := ""
	if !args.Drill {
		query = "isdrill:false"
	}
	if args.Status != "" {
		query += fmt.Sprintf(" and status:%s", args.Status)
	}
	incidents, err := is.QueryIncidents(ctx, incident.QueryIncidentsRequest{
		Query: incident.IncidentsQuery{
			QueryString:    query,
			OrderDirection: "DESC",
			Limit:          args.Limit,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	return incidents, nil
}

var ListIncidents = mcpgrafana.MustTool(
	"list_incidents",
	"List incidents",
	listIncidents,
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

func createIncident(ctx context.Context, args CreateIncidentParams) (*mcp.CallToolResult, error) {
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
	b, err := json.Marshal(incident)
	if err != nil {
		return nil, fmt.Errorf("marshal incident: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var CreateIncident = mcpgrafana.MustTool(
	"create_incident",
	"Create an incident",
	createIncident,
)

type AddActivityToIncidentParams struct {
	IncidentID string `json:"incidentId" jsonschema:"description=The ID of the incident to add the activity to"`
	Body       string `json:"body" jsonschema:"description=The body of the activity. URLs will be parsed and attached as context"`
	EventTime  string `json:"eventTime" jsonschema:"description=The time that the activity occurred. If not provided, the current time will be used"`
}

func addActivityToIncident(ctx context.Context, args AddActivityToIncidentParams) (*mcp.CallToolResult, error) {
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
	b, err := json.Marshal(activity)
	if err != nil {
		return nil, fmt.Errorf("marshal incident: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var AddActivityToIncident = mcpgrafana.MustTool(
	"add_activity_to_incident",
	"Add an activity to an incident",
	addActivityToIncident,
)

func AddIncidentTools(mcp *server.MCPServer) {
	ListIncidents.Register(mcp)
	CreateIncident.Register(mcp)
	AddActivityToIncident.Register(mcp)
}
