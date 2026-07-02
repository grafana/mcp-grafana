package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/grafana/grafana-openapi-client-go/client/dashboards"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// DashboardVersionSummary is a compact representation of a dashboard version
// returned by list_dashboard_versions. The full dashboard JSON (data) is
// intentionally omitted to keep response sizes manageable; use
// get_dashboard_version to retrieve a specific version with its content.
type DashboardVersionSummary struct {
	Version       int64  `json:"version"`
	CreatedBy     string `json:"createdBy,omitempty"`
	Created       string `json:"created,omitempty"`
	Message       string `json:"message,omitempty"`
	ParentVersion int64  `json:"parentVersion,omitempty"`
	RestoredFrom  int64  `json:"restoredFrom,omitempty"`
}

type ListDashboardVersionsParams struct {
	UID   string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
	Limit int64  `json:"limit,omitempty" jsonschema:"description=Maximum number of versions to return. Defaults to all versions when omitted"`
	Start int64  `json:"start,omitempty" jsonschema:"description=Zero-based numeric offset for pagination. Pass the number of versions already retrieved to fetch the next page"`
}

func listDashboardVersions(ctx context.Context, args ListDashboardVersionsParams) ([]DashboardVersionSummary, error) {
	if args.UID == "" {
		return nil, fmt.Errorf("uid is required")
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := dashboards.NewGetDashboardVersionsByUIDParamsWithContext(ctx).
		WithUID(args.UID)

	if args.Limit > 0 {
		params.SetLimit(&args.Limit)
	}
	if args.Start > 0 {
		params.SetStart(&args.Start)
	}

	resp, err := c.Dashboards.GetDashboardVersionsByUID(params)
	if err != nil {
		return nil, fmt.Errorf("list dashboard versions for %q: %w", args.UID, err)
	}

	if resp.Payload == nil {
		return nil, fmt.Errorf("list dashboard versions for %q: empty response from Grafana", args.UID)
	}
	versions := resp.Payload.Versions
	summaries := make([]DashboardVersionSummary, 0, len(versions))
	for _, v := range versions {
		summaries = append(summaries, DashboardVersionSummary{
			Version:       v.Version,
			CreatedBy:     v.CreatedBy,
			Created:       v.Created.String(),
			Message:       v.Message,
			ParentVersion: v.ParentVersion,
			RestoredFrom:  v.RestoredFrom,
		})
	}
	return summaries, nil
}

var ListDashboardVersions = mcpgrafana.MustTool(
	"list_dashboard_versions",
	"List the saved versions of a Grafana dashboard. Returns version metadata such as version number, author, timestamp, and save message. Use get_dashboard_version to retrieve the full content of a specific version.",
	listDashboardVersions,
	mcp.WithTitleAnnotation("List dashboard versions"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type GetDashboardVersionParams struct {
	UID     string `json:"uid" jsonschema:"required,description=The UID of the dashboard"`
	Version int64  `json:"version" jsonschema:"required,description=The version number to retrieve"`
}

// DashboardVersionDetail is the full representation of a single dashboard
// version, including the complete dashboard JSON stored in that version.
type DashboardVersionDetail struct {
	Version       int64          `json:"version"`
	CreatedBy     string         `json:"createdBy,omitempty"`
	Created       string         `json:"created,omitempty"`
	Message       string         `json:"message,omitempty"`
	ParentVersion int64          `json:"parentVersion,omitempty"`
	RestoredFrom  int64          `json:"restoredFrom,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
}

func getDashboardVersion(ctx context.Context, args GetDashboardVersionParams) (*DashboardVersionDetail, error) {
	if args.UID == "" {
		return nil, fmt.Errorf("uid is required")
	}
	if args.Version <= 0 {
		return nil, fmt.Errorf("version must be a positive integer")
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	params := dashboards.NewGetDashboardVersionByUIDParamsWithContext(ctx).
		WithUID(args.UID).
		WithDashboardVersionID(args.Version)
	resp, err := c.Dashboards.GetDashboardVersionByUIDWithParams(params)
	if err != nil {
		return nil, fmt.Errorf("get dashboard version %d for %q: %w", args.Version, args.UID, err)
	}

	v := resp.Payload
	detail := &DashboardVersionDetail{
		Version:       v.Version,
		CreatedBy:     v.CreatedBy,
		Created:       v.Created.String(),
		Message:       v.Message,
		ParentVersion: v.ParentVersion,
		RestoredFrom:  v.RestoredFrom,
	}

	// v.Data is models.JSON (interface{}). Re-encode then decode into
	// map[string]any so the response is always a structured JSON object
	// regardless of how the openapi library decoded the raw value.
	if v.Data != nil {
		b, err := json.Marshal(v.Data)
		if err != nil {
			return nil, fmt.Errorf("marshal dashboard data for version %d of %q: %w", args.Version, args.UID, err)
		}
		var data map[string]any
		if err := json.Unmarshal(b, &data); err != nil {
			return nil, fmt.Errorf("unmarshal dashboard data for version %d of %q: %w", args.Version, args.UID, err)
		}
		detail.Data = data
	}

	return detail, nil
}

var GetDashboardVersion = mcpgrafana.MustTool(
	"get_dashboard_version",
	"Retrieve a specific saved version of a Grafana dashboard by its version number, including the full dashboard JSON stored in that version.",
	getDashboardVersion,
	mcp.WithTitleAnnotation("Get dashboard version"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

