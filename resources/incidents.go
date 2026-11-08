package resources

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/tools"
)

const incidentURIPrefix = "grafana://irm/incidents/"

const (
	// recentIncidents is how far back listed incidents may have started.
	// Large orgs accumulate active incidents nobody resolved (hundreds, over
	// years); the ones worth attaching are the ones being worked on now.
	recentIncidents = 30 * 24 * time.Hour
	// maxActiveIncidents is how many of the most recently created active
	// incidents are considered: the incident API's page size.
	maxActiveIncidents = 100
)

// ActiveIncidents exposes active, non-drill incidents that started in the
// last 30 days as grafana://irm/incidents/{id} resources. Any incident can be
// read, including older and resolved ones.
var ActiveIncidents = Kind{
	template: &mcp.ResourceTemplate{
		URITemplate: incidentURIPrefix + "{id}",
		Name:        "irm_incident",
		Title:       "Grafana IRM incident",
		Description: "Full details of a Grafana IRM incident, including status, severity, labels and custom fields.",
		MIMEType:    "application/json",
	},
	read: readIncident,
	list: listActiveIncidents,
}

func readIncident(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	id, err := idFromURI(uri, incidentURIPrefix)
	if err != nil {
		return nil, err
	}
	if mcpgrafana.IncidentClientFromContext(ctx) == nil {
		return nil, errors.New("no IRM incident client is configured")
	}
	incident, err := tools.Incident(ctx, id)
	var apiErr *mcpgrafana.IncidentAPIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, incident)
}

func listActiveIncidents(ctx context.Context) ([]*mcp.Resource, error) {
	if mcpgrafana.IncidentClientFromContext(ctx) == nil {
		return nil, nil
	}
	result, err := tools.ActiveIncidents(ctx, maxActiveIncidents)
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-recentIncidents)
	var resources []*mcp.Resource
	for _, inc := range result.Incidents {
		started := inc.IncidentStart
		if started == "" {
			started = inc.CreatedTime
		}
		// An unreadable time keeps the incident: better a stale entry than
		// silently hiding a live one.
		if t, err := time.Parse(time.RFC3339, started); err == nil && t.Before(since) {
			continue
		}
		desc := inc.Title + " · active incident"
		if inc.Severity != "" {
			desc += " (" + inc.Severity + ")"
		}
		resources = append(resources, &mcp.Resource{
			URI:         incidentURIPrefix + url.PathEscape(inc.IncidentID),
			Name:        inc.Title,
			Description: desc,
			MIMEType:    "application/json",
		})
	}
	return resources, nil
}
