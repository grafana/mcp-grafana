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

func ListIncidents(ctx context.Context, args ListIncidentsParams) (*mcp.CallToolResult, error) {
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
	b, err := json.Marshal(incidents.Incidents)
	if err != nil {
		return nil, fmt.Errorf("marshal incidents: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

var ListIncidentsTool, ListIncidentsHandler = mcpgrafana.MustTool(
	"list_incidents",
	"List incidents",
	ListIncidents,
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

func CreateIncident(ctx context.Context, args CreateIncidentParams) (*mcp.CallToolResult, error) {
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

var CreateIncidentTool, CreateIncidentHandler = mcpgrafana.MustTool(
	"create_incident",
	"Create an incident",
	CreateIncident,
)

type AddActivityToIncidentParams struct {
	IncidentID string `json:"incidentId" jsonschema:"description=The ID of the incident to add the activity to"`
	Body       string `json:"body" jsonschema:"description=The body of the activity. URLs will be parsed and attached as context"`
	EventTime  string `json:"eventTime" jsonschema:"description=The time that the activity occurred. If not provided, the current time will be used"`
}

func AddActivityToIncident(ctx context.Context, args AddActivityToIncidentParams) (*mcp.CallToolResult, error) {
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

var AddActivityToIncidentTool, AddActivityToIncidentHandler = mcpgrafana.MustTool(
	"add_activity_to_incident",
	"Add an activity to an incident",
	AddActivityToIncident,
)

func AddIncidentTools(mcp *server.MCPServer) {
	mcp.AddTool(ListIncidentsTool, ListIncidentsHandler)
	mcp.AddTool(CreateIncidentTool, CreateIncidentHandler)
	mcp.AddTool(AddActivityToIncidentTool, AddActivityToIncidentHandler)
}
