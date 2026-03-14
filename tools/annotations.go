package tools

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"

	"github.com/grafana/grafana-openapi-client-go/client/annotations"
	"github.com/grafana/grafana-openapi-client-go/models"
)

// GetAnnotationsInput filters annotation search.
type GetAnnotationsInput struct {
	From         *int64   `jsonschema:"description=Epoch ms start time"`
	To           *int64   `jsonschema:"description=Epoch ms end time"`
	Limit        *int64   `jsonschema:"description=Max results default 100"`
	AlertID      *int64   `jsonschema:"description=Deprecated. Use AlertUID"`
	AlertUID     *string  `jsonschema:"description=Filter by alert UID"`
	DashboardID  *int64   `jsonschema:"description=Deprecated. Use DashboardUID"`
	DashboardUID *string  `jsonschema:"description=Filter by dashboard UID"`
	PanelID      *int64   `jsonschema:"description=Filter by panel ID"`
	UserID       *int64   `jsonschema:"description=Filter by creator user ID"`
	Type         *string  `jsonschema:"description=annotation or alert"`
	Tags         []string `jsonschema:"description=Multiple tags allowed tags=tag1&tags=tag2"`
	MatchAny     *bool    `jsonschema:"description=true OR tag match false AND"`
}

// getAnnotations retrieves Grafana annotations using filters.
func getAnnotations(ctx context.Context, args GetAnnotationsInput) (*annotations.GetAnnotationsOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	req := annotations.GetAnnotationsParams{
		From:         args.From,
		To:           args.To,
		Limit:        args.Limit,
		AlertID:      args.AlertID,
		AlertUID:     args.AlertUID,
		DashboardID:  args.DashboardID,
		DashboardUID: args.DashboardUID,
		PanelID:      args.PanelID,
		UserID:       args.UserID,
		Type:         args.Type,
		Tags:         args.Tags,
		MatchAny:     args.MatchAny,
		Context:      ctx,
	}

	resp, err := c.Annotations.GetAnnotations(&req)
	if err != nil {
		return nil, fmt.Errorf("get annotations: %w", err)
	}

	return resp, nil
}

var GetAnnotationsTool = mcpgrafana.MustTool(
	"get_annotations",
	"Fetch Grafana annotations using filters such as dashboard UID, time range, and tags. Use when the user wants to retrieve existing annotations for analysis, debugging, or review purposes. Do not use when you need to create a new annotation (use create_annotation instead). Accepts `dashboardUID` (optional), `from` and `to` (optional time range), `tags` (optional array), and `limit` (optional). e.g., dashboardUID=\"abc123\", from=1609459200000, to=1609545600000, tags=[\"deployment\", \"alert\"]. Raises an error if the time range is invalid or the dashboard UID does not exist.",
	getAnnotations,
	mcp.WithTitleAnnotation("Get Annotations"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// CreateAnnotationInput creates a new annotation, optionally in Graphite format.
type CreateAnnotationInput struct {
	DashboardID  int64          `json:"dashboardId,omitempty"  jsonschema:"description=Deprecated. Use dashboardUID"`
	DashboardUID string         `json:"dashboardUID,omitempty" jsonschema:"description=Preferred dashboard UID"`
	PanelID      int64          `json:"panelId,omitempty"      jsonschema:"description=Panel ID"`
	Time         int64          `json:"time,omitempty"         jsonschema:"description=Start time epoch ms"`
	TimeEnd      int64          `json:"timeEnd,omitempty"      jsonschema:"description=End time epoch ms"`
	Tags         []string       `json:"tags,omitempty"         jsonschema:"description=Optional list of tags"`
	Text         string         `json:"text,omitempty"         jsonschema:"description=Annotation text (required unless format is graphite)"`
	Data         map[string]any `json:"data,omitempty"         jsonschema:"description=Optional JSON payload"`

	// Graphite-specific fields
	Format       string `json:"format,omitempty"       jsonschema:"enum=graphite,description=Set to 'graphite' to create a Graphite-format annotation"`
	What         string `json:"what,omitempty"          jsonschema:"description=Annotation text for Graphite format (required when format is graphite)"`
	When         int64  `json:"when,omitempty"          jsonschema:"description=Epoch ms timestamp for Graphite format"`
	GraphiteData string `json:"graphiteData,omitempty"  jsonschema:"description=Optional string payload for Graphite format"`
}

// createAnnotation sends a POST request to create a Grafana annotation.
// If Format is "graphite", it creates a Graphite-format annotation instead.
func createAnnotation(ctx context.Context, args CreateAnnotationInput) (any, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	if args.Format == "graphite" {
		if args.What == "" {
			return nil, fmt.Errorf("'what' is required when format is 'graphite'")
		}
		req := &models.PostGraphiteAnnotationsCmd{
			What: args.What,
			When: args.When,
			Tags: args.Tags,
			Data: args.GraphiteData,
		}
		resp, err := c.Annotations.PostGraphiteAnnotation(req)
		if err != nil {
			return nil, fmt.Errorf("create graphite annotation: %w", err)
		}
		return resp, nil
	}

	if args.Text == "" {
		return nil, fmt.Errorf("'text' is required for standard annotations")
	}

	req := models.PostAnnotationsCmd{
		DashboardID:  args.DashboardID,
		DashboardUID: args.DashboardUID,
		PanelID:      args.PanelID,
		Time:         args.Time,
		TimeEnd:      args.TimeEnd,
		Tags:         args.Tags,
		Text:         &args.Text,
		Data:         args.Data,
	}

	resp, err := c.Annotations.PostAnnotation(&req)
	if err != nil {
		return nil, fmt.Errorf("create annotation: %w", err)
	}

	return resp, nil
}

var CreateAnnotationTool = mcpgrafana.MustTool(
	"create_annotation",
	"Create a new annotation on a Grafana dashboard or panel to mark events, deployments, or incidents. Use when the user wants to add timestamped notes or markers to visualize important events on charts. Do not use when you need to retrieve existing annotations (use get_annotations instead). Accepts `dashboardUID` (optional), `panelId` (optional), `time` (required timestamp), `text` (required description), and `tags` (optional array). For Graphite-format annotations, set `format` to 'graphite' and provide `what` parameter. e.g., text=\"Deploy v2.1.0\", tags=[\"deployment\", \"production\"]. Raises an error if the dashboard or panel ID is invalid or if required authentication is missing.",
	createAnnotation,
	mcp.WithTitleAnnotation("Create Annotation"),
	mcp.WithIdempotentHintAnnotation(false),
)

// UpdateAnnotationInput updates only the provided fields of an annotation (PATCH semantics).
type UpdateAnnotationInput struct {
	ID      int64          `json:"id"                     jsonschema:"description=Annotation ID to update"`
	Text    *string        `json:"text,omitempty"         jsonschema:"description=New annotation text"`
	Time    *int64         `json:"time,omitempty"         jsonschema:"description=New start time epoch ms"`
	TimeEnd *int64         `json:"timeEnd,omitempty"      jsonschema:"description=New end time epoch ms"`
	Tags    []string       `json:"tags,omitempty"         jsonschema:"description=Tags to replace existing tags"`
	Data    map[string]any `json:"data,omitempty"         jsonschema:"description=Optional JSON payload"`
}

// updateAnnotation updates an annotation using PATCH semantics — only provided fields are modified.
func updateAnnotation(ctx context.Context, args UpdateAnnotationInput) (*annotations.PatchAnnotationOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	id := strconv.FormatInt(args.ID, 10)

	body := &models.PatchAnnotationsCmd{}

	if args.Text != nil {
		body.Text = *args.Text
	}
	if args.Time != nil {
		body.Time = *args.Time
	}
	if args.TimeEnd != nil {
		body.TimeEnd = *args.TimeEnd
	}
	if args.Tags != nil {
		body.Tags = args.Tags
	}
	if args.Data != nil {
		body.Data = args.Data
	}

	resp, err := c.Annotations.PatchAnnotation(id, body)
	if err != nil {
		return nil, fmt.Errorf("update annotation: %w", err)
	}
	return resp, nil
}

var UpdateAnnotationTool = mcpgrafana.MustTool(
	"update_annotation",
	"Update an existing Grafana annotation by modifying specific properties while leaving other fields unchanged. Use when the user wants to edit, correct, or enhance annotation details like text, tags, or time ranges. Do not use when you need to create a new annotation (use create_annotation instead). Accepts `annotation_id` (required), `text` (optional), `tags` (optional array), `time` and `timeEnd` (optional timestamps), e.g., annotation_id=\"12345\", text=\"Updated deployment note\", tags=[\"production\", \"release\"]. Raises an error if the annotation ID does not exist or you lack edit permissions.",
	updateAnnotation,
	mcp.WithTitleAnnotation("Update Annotation"),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithIdempotentHintAnnotation(false),
)

// GetAnnotationTagsInput defines filters for retrieving annotation tags.
type GetAnnotationTagsInput struct {
	Tag   *string `json:"tag,omitempty"   jsonschema:"description=Optional filter by tag name"`
	Limit *string `json:"limit,omitempty" jsonschema:"description=Max results\\, default 100"`
}

func getAnnotationTags(ctx context.Context, args GetAnnotationTagsInput) (*annotations.GetAnnotationTagsOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	req := annotations.GetAnnotationTagsParams{
		Tag:     args.Tag,
		Limit:   args.Limit,
		Context: ctx,
	}

	resp, err := c.Annotations.GetAnnotationTags(&req)
	if err != nil {
		return nil, fmt.Errorf("get annotation tags: %w", err)
	}

	return resp, nil
}

var GetAnnotationTagsTool = mcpgrafana.MustTool(
	"get_annotation_tags",
	"Retrieve annotation tags from Grafana with optional filtering capabilities. Use when the user wants to browse, search, or filter available annotation tags by name or pattern. Accepts `tag` (optional string filter) to narrow results to specific tag names, e.g., \"deployment\" or \"incident\". Do not use when you need to fetch the actual annotations themselves (use get_annotations instead). Returns a list of matching tag names that can be used for annotation filtering. Raises an error if the Grafana API is unreachable or authentication fails.",
	getAnnotationTags,
	mcp.WithTitleAnnotation("Get Annotation Tags"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddAnnotationTools(mcp *server.MCPServer, enableWriteTools bool) {
	GetAnnotationsTool.Register(mcp)
	if enableWriteTools {
		CreateAnnotationTool.Register(mcp)
		UpdateAnnotationTool.Register(mcp)
	}
	GetAnnotationTagsTool.Register(mcp)
}
