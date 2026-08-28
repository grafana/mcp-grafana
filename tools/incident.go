package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// IncidentResult is a single incident as returned by the incident tools.
//
// The embedded incident is inlined into the JSON output, with its raw
// fieldValues replaced by customFields, which resolves each field's name and
// decodes select options into their labels.
type IncidentResult struct {
	*incident.Incident

	// FieldValues shadows the embedded incident's raw list of field UUID/value
	// pairs, which carries no field names and duplicates CustomFields.
	FieldValues []incident.CustomMetadataFieldValue `json:"-"`

	CustomFields []IncidentCustomFieldValue `json:"customFields,omitempty"`
}

// newIncidentResult wraps an incident, optionally reading back its custom field
// values.
func newIncidentResult(ctx context.Context, c *incident.Client, inc *incident.Incident, includeCustomFields bool) (*IncidentResult, error) {
	result := &IncidentResult{Incident: inc}
	if !includeCustomFields {
		return result, nil
	}
	customFields, err := incidentCustomFieldValues(ctx, c, inc.IncidentID)
	if err != nil {
		return nil, err
	}
	result.CustomFields = customFields
	return result, nil
}

type ListIncidentsParams struct {
	Limit               int    `json:"limit" jsonschema:"default=10,description=The maximum number of incidents to return"`
	Drill               bool   `json:"drill" jsonschema:"description=Whether to include drill incidents"`
	Status              string `json:"status" jsonschema:"description=The status of the incidents to include. Valid values: 'active'\\, 'resolved'"`
	IncludeCustomFields bool   `json:"includeCustomFields" jsonschema:"description=Whether to include each incident's custom field values. Off by default because it costs an extra request and makes the response considerably larger"`
}

type incidentPreviewSummary struct {
	IncidentID    string                     `json:"incidentId"`
	Title         string                     `json:"title"`
	Status        string                     `json:"status"`
	Severity      string                     `json:"severity"`
	CreatedTime   string                     `json:"createdTime,omitempty"`
	ModifiedTime  string                     `json:"modifiedTime,omitempty"`
	IncidentStart string                     `json:"incidentStart,omitempty"`
	IsDrill       bool                       `json:"isDrill,omitempty"`
	CustomFields  []IncidentCustomFieldValue `json:"customFields,omitempty"`
}

type ListIncidentsResult struct {
	Incidents []incidentPreviewSummary `json:"incidents"`
	HasMore   bool                     `json:"hasMore"`
}

// summarizeIncidentPreviews trims incident previews down to the fields worth
// returning. customFieldsByUUID is nil unless custom fields were requested.
func summarizeIncidentPreviews(previews []incident.IncidentPreview, customFieldsByUUID map[string]incident.CustomMetadataField) []incidentPreviewSummary {
	result := make([]incidentPreviewSummary, 0, len(previews))
	for _, p := range previews {
		summary := incidentPreviewSummary{
			IncidentID:    p.IncidentID,
			Title:         p.Title,
			Status:        p.Status,
			Severity:      p.SeverityLabel,
			CreatedTime:   p.CreatedTime,
			ModifiedTime:  p.ModifiedTime,
			IncidentStart: p.IncidentStart,
			IsDrill:       p.IsDrill,
		}
		if customFieldsByUUID != nil {
			summary.CustomFields = summarizeIncidentCustomFieldValues(customFieldsByUUID, p.FieldValues)
		}
		result = append(result, summary)
	}
	return result
}

func listIncidents(ctx context.Context, args ListIncidentsParams) (*ListIncidentsResult, error) {
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
		IncludeCustomFieldValues: args.IncludeCustomFields,
	})
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}

	// Previews carry field UUIDs only, so the definitions are needed to name
	// the fields and decode their values.
	var customFieldsByUUID map[string]incident.CustomMetadataField
	if args.IncludeCustomFields {
		fields, err := fetchIncidentCustomFields(ctx, c)
		if err != nil {
			return nil, err
		}
		customFieldsByUUID = indexIncidentCustomFields(fields)
	}

	return &ListIncidentsResult{
		Incidents: summarizeIncidentPreviews(incidents.IncidentPreviews, customFieldsByUUID),
		HasMore:   incidents.Cursor.HasMore,
	}, nil
}

var ListIncidents = mcpgrafana.MustTool(
	"list_incidents",
	"List Grafana incidents. Allows filtering by status ('active', 'resolved') and optionally including drill incidents. Returns a preview list with basic details, and custom field values if requested.",
	listIncidents,
	mcp.WithTitleAnnotation("List incidents"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type CreateIncidentParams struct {
	Title         string                     `json:"title" jsonschema:"required,description=The title of the incident"`
	Severity      string                     `json:"severity" jsonschema:"required,description=The severity of the incident"`
	RoomPrefix    string                     `json:"roomPrefix" jsonschema:"required,description=The prefix of the room to create the incident in"`
	IsDrill       bool                       `json:"isDrill" jsonschema:"description=Whether the incident is a drill incident"`
	Status        string                     `json:"status" jsonschema:"description=The status of the incident"`
	AttachCaption string                     `json:"attachCaption" jsonschema:"description=The caption of the attachment"`
	AttachURL     string                     `json:"attachUrl" jsonschema:"description=The URL of the attachment"`
	Labels        []incident.IncidentLabel   `json:"labels" jsonschema:"description=The labels to add to the incident"`
	CustomFields  []IncidentCustomFieldInput `json:"customFields" jsonschema:"description=Custom field values to set on the new incident. Use list_incident_custom_fields to discover the available fields and their valid values"`
}

// createIncident creates an incident and then records any custom field values.
//
// Custom fields cannot be set as part of the create request, so they are
// applied afterwards. If applying one fails the incident has already been
// created, and the error names the field that failed.
func createIncident(ctx context.Context, args CreateIncidentParams) (*IncidentResult, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	is := incident.NewIncidentsService(c)
	created, err := is.CreateIncident(ctx, incident.CreateIncidentRequest{
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

	if err := applyIncidentCustomFields(ctx, c, created.Incident.IncidentID, args.CustomFields); err != nil {
		return nil, err
	}
	return newIncidentResult(ctx, c, &created.Incident, len(args.CustomFields) > 0)
}

var CreateIncident = mcpgrafana.MustTool(
	"create_incident",
	"Create a new Grafana incident. Requires title, severity, and room prefix. Allows setting status, labels and custom fields. This tool should be used judiciously and sparingly, and only after confirmation from the user, as it may notify or alarm lots of people.",
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
		UpdateIncident.Register(mcp)
	}
	GetIncident.Register(mcp)
	ListIncidentCustomFields.Register(mcp)
}

type GetIncidentParams struct {
	ID string `json:"id" jsonschema:"required,description=The ID of the incident to retrieve"`
}

func getIncident(ctx context.Context, args GetIncidentParams) (*IncidentResult, error) {
	c := mcpgrafana.IncidentClientFromContext(ctx)
	is := incident.NewIncidentsService(c)

	incidentResp, err := is.GetIncident(ctx, incident.GetIncidentRequest{
		IncidentID: args.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("get incident by ID: %w", err)
	}

	return newIncidentResult(ctx, c, &incidentResp.Incident, true)
}

var GetIncident = mcpgrafana.MustTool(
	"get_incident",
	"Get a single incident by ID. Returns the full incident details including title, status, severity, labels, custom fields, timestamps, and other metadata.",
	getIncident,
	mcp.WithTitleAnnotation("Get incident details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

type UpdateIncidentParams struct {
	IncidentID   string                     `json:"incidentId" jsonschema:"required,description=The ID of the incident to update"`
	Status       string                     `json:"status" jsonschema:"description=The new status of the incident. Valid values: 'active'\\, 'resolved'"`
	Severity     string                     `json:"severity" jsonschema:"description=The new severity of the incident\\, e.g. 'minor'\\, 'major'\\, 'critical'"`
	Title        string                     `json:"title" jsonschema:"description=The new title of the incident"`
	CustomFields []IncidentCustomFieldInput `json:"customFields" jsonschema:"description=Custom field values to set. Only the listed fields are changed. Use list_incident_custom_fields to discover the available fields and their valid values"`
}

// updateIncident applies the requested changes to an incident.
//
// Grafana Incident has no single 'update incident' endpoint: status, severity,
// title and each custom field have their own endpoint. Fields are therefore
// applied one at a time. If a later call fails, earlier changes have already
// been applied, so the error names the field that failed.
func updateIncident(ctx context.Context, args UpdateIncidentParams) (*IncidentResult, error) {
	if args.IncidentID == "" {
		return nil, errors.New("incidentId is required")
	}
	if args.Status == "" && args.Severity == "" && args.Title == "" && len(args.CustomFields) == 0 {
		return nil, errors.New("at least one of status, severity, title or customFields must be provided")
	}

	c := mcpgrafana.IncidentClientFromContext(ctx)
	is := incident.NewIncidentsService(c)

	var updated *incident.Incident
	if args.Status != "" {
		resp, err := is.UpdateStatus(ctx, incident.UpdateStatusRequest{
			IncidentID: args.IncidentID,
			Status:     args.Status,
		})
		if err != nil {
			return nil, fmt.Errorf("update incident status: %w", err)
		}
		updated = &resp.Incident
	}
	if args.Severity != "" {
		resp, err := is.UpdateSeverity(ctx, incident.UpdateSeverityRequest{
			IncidentID: args.IncidentID,
			Severity:   args.Severity,
		})
		if err != nil {
			return nil, fmt.Errorf("update incident severity: %w", err)
		}
		updated = &resp.Incident
	}
	if args.Title != "" {
		resp, err := is.UpdateTitle(ctx, incident.UpdateTitleRequest{
			IncidentID: args.IncidentID,
			Title:      args.Title,
		})
		if err != nil {
			return nil, fmt.Errorf("update incident title: %w", err)
		}
		updated = &resp.Incident
	}
	if err := applyIncidentCustomFields(ctx, c, args.IncidentID, args.CustomFields); err != nil {
		return nil, err
	}

	// Custom field writes don't return the incident, so fetch it when nothing
	// else did.
	if updated == nil {
		resp, err := is.GetIncident(ctx, incident.GetIncidentRequest{IncidentID: args.IncidentID})
		if err != nil {
			return nil, fmt.Errorf("get updated incident: %w", err)
		}
		updated = &resp.Incident
	}
	return newIncidentResult(ctx, c, updated, len(args.CustomFields) > 0)
}

var UpdateIncident = mcpgrafana.MustTool(
	"update_incident",
	"Update an existing Grafana incident by ID. Allows changing the status ('active' or 'resolved'), the severity, the title, and custom field values. Only the provided fields are changed. Use this to resolve an incident, to correct its severity or title, or to fill in custom fields as part of an on-call workflow.",
	updateIncident,
	mcp.WithTitleAnnotation("Update incident"),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)
