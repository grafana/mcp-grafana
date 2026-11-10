package tools

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"

	"github.com/grafana/grafana-openapi-client-go/client/annotations"
	"github.com/grafana/grafana-openapi-client-go/models"
)

// GetAnnotationsInput filters annotation search.
type GetAnnotationsInput struct {
	From         *int64   `json:"from,omitempty" jsonschema:"description=Epoch ms start time"`
	To           *int64   `json:"to,omitempty" jsonschema:"description=Epoch ms end time"`
	Limit        *int64   `json:"limit,omitempty" jsonschema:"description=Max results default 100"`
	AlertUID     *string  `json:"alertUid,omitempty" jsonschema:"description=Filter by alert UID"`
	DashboardUID *string  `json:"dashboardUid,omitempty" jsonschema:"description=Filter by dashboard UID"`
	PanelID      *int64   `json:"panelId,omitempty" jsonschema:"description=Filter by panel ID"`
	UserID       *int64   `json:"userId,omitempty" jsonschema:"description=Filter by creator user ID"`
	Type         *string  `json:"type,omitempty" jsonschema:"description=annotation or alert"`
	Tags         []string `json:"tags,omitempty" jsonschema:"description=Filter by tags. Multiple tags allowed; use matchAny to control AND/OR logic"`
	MatchAny     *bool    `json:"matchAny,omitempty" jsonschema:"description=If true\\, match any tag (OR). If false\\, match all tags (AND). Default: false"`
}

// getAnnotations retrieves Grafana annotations using filters.
func getAnnotations(ctx context.Context, args GetAnnotationsInput) (*annotations.GetAnnotationsOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	req := annotations.GetAnnotationsParams{
		From:         args.From,
		To:           args.To,
		Limit:        args.Limit,
		AlertUID:     args.AlertUID,
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
	"Fetch Grafana annotations using filters such as dashboard UID, time range and tags.",
	getAnnotations,
	mcpgrafana.WithTitleAnnotation("Get Annotations"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("annotations:read"),
)

// CreateAnnotationInput creates a new annotation, optionally in Graphite format.
type CreateAnnotationInput struct {
	DashboardUID string         `json:"dashboardUid,omitempty" jsonschema:"description=Dashboard UID"`
	PanelID      int64          `json:"panelId,omitempty"      jsonschema:"description=Panel ID"`
	Time         int64          `json:"time,omitempty"         jsonschema:"description=Start time epoch ms. Alternatively set 'timeISO'."`
	TimeEnd      int64          `json:"timeEnd,omitempty"      jsonschema:"description=End time epoch ms. Alternatively set 'timeEndISO'."`
	TimeISO      string         `json:"timeISO,omitempty"      jsonschema:"description=Start time as an ISO-8601 timestamp (e.g. 2026-01-02T15:04:05Z; UTC if no offset is given) or a date (2026-01-02). Alternative to 'time' so no manual epoch-ms conversion is needed; if both are set 'timeISO' takes precedence."`
	TimeEndISO   string         `json:"timeEndISO,omitempty"   jsonschema:"description=End time as an ISO-8601 timestamp or date\\, read the same way. Alternative to 'timeEnd'; if both are set 'timeEndISO' takes precedence."`
	Tags         []string       `json:"tags,omitempty"         jsonschema:"description=Optional list of tags"`
	Text         string         `json:"text,omitempty"         jsonschema:"description=Annotation text (required unless format is graphite)"`
	Data         map[string]any `json:"data,omitempty"         jsonschema:"description=Optional JSON payload"`

	// Graphite-specific fields
	Format       string `json:"format,omitempty"       jsonschema:"enum=graphite,description=Set to 'graphite' to create a Graphite-format annotation"`
	What         string `json:"what,omitempty"          jsonschema:"description=Annotation text for Graphite format (required when format is graphite)"`
	When         int64  `json:"when,omitempty"          jsonschema:"description=Epoch ms timestamp for Graphite format"`
	GraphiteData string `json:"graphiteData,omitempty"  jsonschema:"description=Optional string payload for Graphite format"`
}

// parseISOToEpochMs parses an ISO-8601 timestamp into epoch milliseconds: an
// RFC3339 timestamp with an offset or Z, a timestamp without an offset, or a
// bare date, each with or without sub-second precision where that applies. A
// value without an offset is taken as UTC, as elsewhere in this package.
func parseISOToEpochMs(s string) (int64, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("expected an ISO-8601 timestamp (2026-01-02T15:04:05Z; UTC is assumed without an offset) or date (2026-01-02)")
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
			When: args.When / 1000, // the Graphite endpoint takes epoch seconds
			Tags: args.Tags,
			Data: args.GraphiteData,
		}
		resp, err := c.Annotations.PostGraphiteAnnotationWithParams(
			annotations.NewPostGraphiteAnnotationParamsWithContext(ctx).WithBody(req),
		)
		if err != nil {
			return nil, fmt.Errorf("create graphite annotation: %w", err)
		}
		return resp, nil
	}

	if args.Text == "" {
		return nil, fmt.Errorf("'text' is required for standard annotations")
	}

	// Accept ISO-8601 timestamps as an alternative to epoch ms, so the
	// caller does not have to convert. timeISO/timeEndISO take precedence.
	timeMs := args.Time
	if args.TimeISO != "" {
		ms, err := parseISOToEpochMs(args.TimeISO)
		if err != nil {
			return nil, fmt.Errorf("invalid timeISO %q: %w", args.TimeISO, err)
		}
		timeMs = ms
	}
	timeEndMs := args.TimeEnd
	if args.TimeEndISO != "" {
		ms, err := parseISOToEpochMs(args.TimeEndISO)
		if err != nil {
			return nil, fmt.Errorf("invalid timeEndISO %q: %w", args.TimeEndISO, err)
		}
		timeEndMs = ms
	}

	req := models.PostAnnotationsCmd{
		DashboardUID: args.DashboardUID,
		PanelID:      args.PanelID,
		Time:         timeMs,
		TimeEnd:      timeEndMs,
		Tags:         args.Tags,
		Text:         &args.Text,
		Data:         args.Data,
	}

	resp, err := c.Annotations.PostAnnotationWithParams(
		annotations.NewPostAnnotationParamsWithContext(ctx).WithBody(&req),
	)
	if err != nil {
		return nil, fmt.Errorf("create annotation: %w", err)
	}

	// Echo the stored annotation so the agent can confirm the write — time,
	// text and tags exactly as Grafana saved them, including server-filled
	// defaults (e.g. time defaulting to now) — without a follow-up
	// get_annotations. Best-effort: the create has already succeeded, and a
	// token with only annotations:create can still create, so the result
	// simply carries no annotation when the read-back is unavailable.
	result := &CreateAnnotationResult{}
	if resp != nil {
		result.Payload = resp.Payload
	}
	if result.Payload != nil && result.Payload.ID != nil {
		result.Annotation = readBackAnnotation(ctx, c, strconv.FormatInt(*result.Payload.ID, 10))
	}

	return result, nil
}

// CreateAnnotationResult is the result of create_annotation for a standard
// annotation. Payload is the create response in its existing shape (the new id
// and a message), so callers reading the id are unaffected; Annotation is the
// stored annotation, present when it could be read back.
type CreateAnnotationResult struct {
	Payload    *models.PostAnnotationOKBody `json:"Payload"`
	Annotation *models.Annotation           `json:"annotation,omitempty"`
}

// UpdateAnnotationResult is the result of update_annotation. Payload is the
// patch response in its existing shape (a status message); Annotation is the
// stored annotation after the update, present when it could be read back.
type UpdateAnnotationResult struct {
	Payload    *models.SuccessResponseBody `json:"Payload"`
	Annotation *models.Annotation          `json:"annotation,omitempty"`
}

// readBackAnnotation fetches an annotation that was just written. It passes the
// request context so the read goes to the same org, with the same headers,
// cancellation and tracing, as the write it confirms. It returns nil when the
// annotation cannot be read.
func readBackAnnotation(ctx context.Context, c *mcpgrafana.GrafanaClient, id string) *models.Annotation {
	got, err := c.Annotations.GetAnnotationByIDWithParams(
		annotations.NewGetAnnotationByIDParamsWithContext(ctx).WithAnnotationID(id),
	)
	if err != nil || got == nil {
		return nil
	}
	return got.Payload
}

var CreateAnnotationTool = mcpgrafana.MustTool(
	"create_annotation",
	"Create a new annotation on a dashboard or panel. Times may be given as epoch ms ('time'/'timeEnd') or as ISO-8601 timestamps ('timeISO'/'timeEndISO', e.g. 2026-01-02T15:04:05Z) — no manual epoch conversion needed. Set format to 'graphite' and provide 'what' for Graphite-format annotations. For standard annotations the result also carries the stored annotation under 'annotation', so no follow-up read is needed to confirm it.",
	createAnnotation,
	mcpgrafana.WithTitleAnnotation("Create Annotation"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("annotations:create"),
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
func updateAnnotation(ctx context.Context, args UpdateAnnotationInput) (any, error) {
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

	resp, err := c.Annotations.PatchAnnotationWithParams(
		annotations.NewPatchAnnotationParamsWithContext(ctx).WithAnnotationID(id).WithBody(body),
	)
	if err != nil {
		return nil, fmt.Errorf("update annotation: %w", err)
	}

	// PatchAnnotationOK carries only a status message, so an agent cannot
	// see which fields now hold which values and typically issues a verify
	// get_annotations. Add the full stored annotation to the result.
	// Best-effort: the patch has already succeeded, so the result simply
	// carries no annotation when the read-back is unavailable.
	result := &UpdateAnnotationResult{Annotation: readBackAnnotation(ctx, c, id)}
	if resp != nil {
		result.Payload = resp.Payload
	}

	return result, nil
}

var UpdateAnnotationTool = mcpgrafana.MustTool(
	"update_annotation",
	"Updates the provided properties of an annotation by ID. Only fields included in the request are modified; omitted fields are left unchanged. The result also carries the updated annotation under 'annotation', so no follow-up read is needed to confirm the change.",
	updateAnnotation,
	mcpgrafana.WithTitleAnnotation("Update Annotation"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	// Grafana finds the annotation through a search filtered by
	// annotations:read before checking annotations:write; without read it
	// answers 404.
	mcpgrafana.RequiresPermissions("annotations:read", "annotations:write"),
)

// DeleteAnnotationInput identifies the annotation to delete.
type DeleteAnnotationInput struct {
	ID int64 `json:"id" jsonschema:"required,description=Annotation ID to delete"`
}

// deleteAnnotation permanently removes an annotation by ID.
func deleteAnnotation(ctx context.Context, args DeleteAnnotationInput) (*annotations.DeleteAnnotationByIDOK, error) {
	// The jsonschema "required" is advisory — nothing rejects a call that omits
	// id, and Grafana answers the resulting /api/annotations/0 with a permission
	// error that reads as a real access problem. Say what is actually wrong.
	if args.ID <= 0 {
		return nil, fmt.Errorf("delete annotation: id is required and must be positive, got %d", args.ID)
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	id := strconv.FormatInt(args.ID, 10)

	resp, err := c.Annotations.DeleteAnnotationByIDWithParams(
		annotations.NewDeleteAnnotationByIDParamsWithContext(ctx).WithAnnotationID(id),
	)
	if err != nil {
		return nil, fmt.Errorf("delete annotation: %w", err)
	}
	return resp, nil
}

var DeleteAnnotationTool = mcpgrafana.MustTool(
	"delete_annotation",
	"Permanently delete an annotation by ID. The annotation cannot be recovered afterwards.",
	deleteAnnotation,
	mcpgrafana.WithTitleAnnotation("Delete Annotation"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("annotations:delete"),
)

// GetAnnotationTagsInput defines filters for retrieving annotation tags.
type GetAnnotationTagsInput struct {
	Tag   *string `json:"tag,omitempty"   jsonschema:"description=Optional filter by tag name"`
	Limit *int64  `json:"limit,omitempty" jsonschema:"minimum=1,description=Max results\\, default 100"`
}

func getAnnotationTags(ctx context.Context, args GetAnnotationTagsInput) (*annotations.GetAnnotationTagsOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	var limitStr *string
	if args.Limit != nil {
		s := strconv.FormatInt(*args.Limit, 10)
		limitStr = &s
	}

	req := annotations.GetAnnotationTagsParams{
		Tag:     args.Tag,
		Limit:   limitStr,
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
	"Returns annotation tags with optional filtering by tag name. Only the provided filters are applied.",
	getAnnotationTags,
	mcpgrafana.WithTitleAnnotation("Get Annotation Tags"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions("annotations:read"),
)

func AddAnnotationTools(s *mcp.Server, enableWriteTools bool) {
	GetAnnotationsTool.Register(s)
	if enableWriteTools {
		CreateAnnotationTool.Register(s)
		UpdateAnnotationTool.Register(s)
		DeleteAnnotationTool.Register(s)
	}
	GetAnnotationTagsTool.Register(s)
}
