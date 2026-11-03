package resources

import (
	"context"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/tools"
)

const incidentURIPrefix = "grafana://incidents/"

// maxActiveIncidents caps the active incidents listed. There are normally a
// handful; this only guards against something pathological.
const maxActiveIncidents = 100

// ActiveIncidents exposes each active, non-drill incident as a
// grafana://incidents/{id} resource. Any incident can be read, including
// resolved ones.
var ActiveIncidents = Kind{
	template: &mcp.ResourceTemplate{
		URITemplate: incidentURIPrefix + "{id}",
		Name:        "incident",
		Title:       "Grafana incident",
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
	incident, err := tools.Incident(ctx, id)
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
	resources := make([]*mcp.Resource, 0, len(result.Incidents))
	for _, inc := range result.Incidents {
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
