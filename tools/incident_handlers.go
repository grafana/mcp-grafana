package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

func incidentsRead(ctx context.Context, args IncidentReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("incidents_read: %w", err)
	}

	switch args.Operation {
	case "list":
		return listIncidents(ctx, ListIncidentsParams{Limit: args.Limit, Drill: args.Drill, Status: args.Status})
	case "get":
		return getIncident(ctx, GetIncidentParams{ID: args.IncidentID})
	default:
		// Unreachable once validate() has passed; kept for defense in depth.
		return nil, fmt.Errorf("incidents_read: unknown operation %q", args.Operation)
	}
}

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

func summarizeIncidentPreviews(previews []incident.IncidentPreview) []incidentPreviewSummary {
	result := make([]incidentPreviewSummary, 0, len(previews))
	for _, p := range previews {
		result = append(result, incidentPreviewSummary{
			IncidentID:    p.IncidentID,
			Title:         p.Title,
			Status:        p.Status,
			Severity:      p.SeverityLabel,
			CreatedTime:   p.CreatedTime,
			ModifiedTime:  p.ModifiedTime,
			IncidentStart: p.IncidentStart,
			IsDrill:       p.IsDrill,
		})
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
	})
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	return &ListIncidentsResult{
		Incidents: summarizeIncidentPreviews(incidents.IncidentPreviews),
		HasMore:   incidents.Cursor.HasMore,
	}, nil
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

func incidentsWrite(ctx context.Context, args IncidentWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("incidents_write: %w", err)
	}

	switch args.Operation {
	case "create":
		return createIncident(ctx, CreateIncidentParams{
			Title:         args.Title,
			Severity:      args.Severity,
			RoomPrefix:    args.RoomPrefix,
			IsDrill:       args.IsDrill,
			Status:        args.Status,
			AttachCaption: args.AttachCaption,
			AttachURL:     args.AttachURL,
			Labels:        args.Labels,
		})
	case "update":
		return updateIncident(ctx, UpdateIncidentParams{
			IncidentID: args.IncidentID,
			Status:     args.Status,
			Severity:   args.Severity,
			Title:      args.Title,
		})
	case "add_activity":
		return addActivityToIncident(ctx, AddActivityToIncidentParams{
			IncidentID: args.IncidentID,
			Body:       args.Body,
			EventTime:  args.EventTime,
		})
	default:
		// Unreachable once validate() has passed; kept for defense in depth.
		return nil, fmt.Errorf("incidents_write: unknown operation %q", args.Operation)
	}
}

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

type UpdateIncidentParams struct {
	IncidentID string `json:"incidentId" jsonschema:"required,description=The ID of the incident to update"`
	Status     string `json:"status" jsonschema:"description=The new status of the incident. Valid values: 'active'\\, 'resolved'"`
	Severity   string `json:"severity" jsonschema:"description=The new severity of the incident\\, e.g. 'minor'\\, 'major'\\, 'critical'"`
	Title      string `json:"title" jsonschema:"description=The new title of the incident"`
}

// updateIncident applies the requested changes to an incident.
//
// Grafana Incident has no single 'update incident' endpoint: status, severity
// and title each have their own endpoint. Fields are therefore applied one at a
// time, and the incident returned by the last successful call is returned. If a
// later call fails, earlier changes have already been applied, so the error
// names the field that failed.
func updateIncident(ctx context.Context, args UpdateIncidentParams) (*incident.Incident, error) {
	if args.IncidentID == "" {
		return nil, errors.New("incidentId is required")
	}
	if args.Status == "" && args.Severity == "" && args.Title == "" {
		return nil, errors.New("at least one of status, severity or title must be provided")
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
	return updated, nil
}
