package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/grafana/grafana-openapi-client-go/client/dashboards"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// DashboardVersionSummary is a compact representation of a dashboard version
// returned by list_dashboard_versions. The full dashboard JSON is omitted; use
// get_dashboard_by_uid with version to retrieve a specific snapshot.
type DashboardVersionSummary struct {
	Version   int64  `json:"version"`
	CreatedBy string `json:"createdBy,omitempty"`
	Created   string `json:"created,omitempty"`
	Message   string `json:"message,omitempty"`
}

type ListDashboardVersionsParams struct {
	UID   string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of versions to return"`
	Start int    `json:"start,omitempty" jsonschema:"description=Version to start from. Only versions at or below this number are returned"`
}

func listDashboardVersions(ctx context.Context, args ListDashboardVersionsParams) ([]DashboardVersionSummary, error) {
	if args.UID == "" {
		return nil, fmt.Errorf("uid is required")
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := dashboards.NewGetDashboardVersionsByUIDParamsWithContext(ctx).
		WithUID(args.UID)

	if args.Limit > 0 {
		limit := int64(args.Limit)
		params.SetLimit(&limit)
	}
	if args.Start > 0 {
		start := int64(args.Start)
		params.SetStart(&start)
	}

	resp, err := c.Dashboards.GetDashboardVersionsByUID(params, withDashboardVersionsDualFormatReader())
	if err != nil {
		return nil, fmt.Errorf("list dashboard versions for %q: %w", args.UID, err)
	}

	if resp.Payload == nil {
		return nil, fmt.Errorf("list dashboard versions for %q: empty response from Grafana", args.UID)
	}

	versions := resp.Payload.Versions
	summaries := make([]DashboardVersionSummary, 0, len(versions))
	for _, v := range versions {
		if v == nil {
			continue
		}
		summaries = append(summaries, DashboardVersionSummary{
			Version:   v.Version,
			CreatedBy: v.CreatedBy,
			Created:   formatDashboardVersionTime(v.Created),
			Message:   v.Message,
		})
	}
	return summaries, nil
}

var ListDashboardVersions = mcpgrafana.MustTool(
	"list_dashboard_versions",
	"List saved versions of a Grafana dashboard. Returns compact metadata: version number, author, timestamp, and save message. Use get_dashboard_by_uid with version to fetch a snapshot.",
	listDashboardVersions,
	mcp.WithTitleAnnotation("List dashboard versions"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

func fetchDashboardVersion(ctx context.Context, uid string, version int64) (*DashboardResponse, error) {
	if uid == "" {
		return nil, fmt.Errorf("uid is required")
	}
	if version <= 0 {
		return nil, fmt.Errorf("version must be a positive integer")
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := dashboards.NewGetDashboardVersionByUIDParamsWithContext(ctx).
		WithUID(uid).
		WithDashboardVersionID(version)
	resp, err := c.Dashboards.GetDashboardVersionByUIDWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("get dashboard version %d for %q: %w", version, uid, err)
	}
	if resp.Payload == nil {
		return nil, fmt.Errorf("get dashboard version %d for %q: empty response from Grafana", version, uid)
	}

	v := resp.Payload
	spec, err := dashboardVersionData(v.Data)
	if err != nil {
		return nil, fmt.Errorf("decode dashboard data for version %d of %q: %w", version, uid, err)
	}

	return &DashboardResponse{
		Dashboard: spec,
		Meta: &models.DashboardMeta{
			Version:   v.Version,
			CreatedBy: v.CreatedBy,
			Created:   v.Created,
		},
		IsV2: isV2DashboardJSON(spec),
	}, nil
}

func dashboardVersionData(data models.JSON) (map[string]any, error) {
	if data == nil {
		return nil, fmt.Errorf("missing dashboard JSON")
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var spec map[string]any
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, err
	}
	return spec, nil
}

func formatDashboardVersionTime(created strfmt.DateTime) string {
	t := time.Time(created)
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func withDashboardVersionsDualFormatReader() dashboards.ClientOption {
	return func(op *runtime.ClientOperation) {
		op.Reader = dashboardVersionsDualFormatReader{}
	}
}

// dashboardVersionsDualFormatReader accepts both Grafana 12+
// {versions:[...]} and Grafana 11's bare JSON array.
type dashboardVersionsDualFormatReader struct{}

func (dashboardVersionsDualFormatReader) ReadResponse(response runtime.ClientResponse, consumer runtime.Consumer) (interface{}, error) {
	if response.Code() != 200 {
		var reader dashboards.GetDashboardVersionsByUIDReader
		return reader.ReadResponse(response, consumer)
	}
	body, err := io.ReadAll(response.Body())
	if err != nil {
		return nil, err
	}
	versions, err := decodeDashboardVersionList(body)
	if err != nil {
		return nil, fmt.Errorf("decode dashboard versions: %w", err)
	}
	return &dashboards.GetDashboardVersionsByUIDOK{
		Payload: &models.DashboardVersionResponseMeta{Versions: versions},
	}, nil
}

func decodeDashboardVersionList(body []byte) ([]*models.DashboardVersionMeta, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	switch body[0] {
	case '[':
		var versions []*models.DashboardVersionMeta
		if err := json.Unmarshal(body, &versions); err != nil {
			return nil, err
		}
		return versions, nil
	case '{':
		var wrapped models.DashboardVersionResponseMeta
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, err
		}
		return wrapped.Versions, nil
	default:
		return nil, fmt.Errorf("unexpected dashboard versions payload")
	}
}
