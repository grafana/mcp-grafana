package resources

import (
	"context"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grafana/mcp-grafana/v2/tools"
)

const scheduleURIPrefix = "grafana://irm/schedules/"

// Schedules exposes each IRM on-call schedule as a
// grafana://irm/schedules/{id} resource. Reading one includes who is on call
// now.
var Schedules = Kind{
	template: &mcp.ResourceTemplate{
		URITemplate: scheduleURIPrefix + "{id}",
		Name:        "irm_schedule",
		Title:       "Grafana IRM schedule",
		Description: "A Grafana IRM on-call schedule and the users currently on call for it.",
		MIMEType:    "application/json",
	},
	read: readSchedule,
	list: listSchedules,
}

func readSchedule(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	id, err := idFromURI(uri, scheduleURIPrefix)
	if err != nil {
		return nil, err
	}
	schedule, err := tools.OnCallSchedule(ctx, id)
	if err != nil {
		return nil, err
	}
	current, err := tools.CurrentOnCall(ctx, id)
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, struct {
		*tools.ScheduleSummary
		OnCallNow []*tools.OnCallUser `json:"onCallNow"`
	}{schedule, current.Users})
}

func listSchedules(ctx context.Context) ([]*mcp.Resource, error) {
	schedules, err := tools.OnCallSchedules(ctx)
	if err != nil {
		return nil, err
	}
	resources := make([]*mcp.Resource, 0, len(schedules))
	for _, s := range schedules {
		desc := s.Name + " · on-call schedule"
		if s.Timezone != "" {
			desc += " (" + s.Timezone + ")"
		}
		resources = append(resources, &mcp.Resource{
			URI:         scheduleURIPrefix + url.PathEscape(s.ID),
			Name:        s.Name,
			Description: desc,
			MIMEType:    "application/json",
		})
	}
	return resources, nil
}
