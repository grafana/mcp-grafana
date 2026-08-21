package tools

import (
	"context"
	"fmt"
	"strconv"

	mcpgrafana "github.com/grafana/mcp-grafana"

	"github.com/grafana/grafana-openapi-client-go/client/annotations"
	"github.com/grafana/grafana-openapi-client-go/models"
)

func annotationsRead(ctx context.Context, args AnnotationsReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("annotations_read: %w", err)
	}
	return annotationsReadDispatch(ctx, args.Operation, args.annotationsReadRequest)
}

// annotationsReadDispatch is the only place 'list' and 'tags' are
// implemented. annotations_write deliberately does NOT call this as a
// fallback dispatcher (contrast with a single-name dual-schema tool like
// alerting_manage_rules, where the read+write variant's handler would): see
// AnnotationsWriteParams.validate for why a write tool delegating dispatch
// to a sibling read tool would incorrectly grant it read capability it
// doesn't advertise. It still returns the shared ErrUnknownOperation
// sentinel for operations it doesn't own, both for symmetry with the
// dispatch functions that DO get called this way (see
// tools/operation_validation_test.go's TestDelegation_SingleNameDualSchema)
// and in case a future domain's read/write split isn't fully disjoint.
func annotationsReadDispatch(ctx context.Context, operation string, r annotationsReadRequest) (any, error) {
	switch operation {
	case "list":
		return getAnnotations(ctx, r)
	case "tags":
		return getAnnotationTags(ctx, r)
	default:
		return nil, ErrUnknownOperation
	}
}

// getAnnotations retrieves Grafana annotations using filters.
func getAnnotations(ctx context.Context, r annotationsReadRequest) (*annotations.GetAnnotationsOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	req := annotations.GetAnnotationsParams{
		From:         r.From,
		To:           r.To,
		Limit:        r.Limit,
		AlertUID:     r.AlertUID,
		DashboardUID: r.DashboardUID,
		PanelID:      r.PanelID,
		UserID:       r.UserID,
		Type:         r.Type,
		Tags:         r.Tags,
		MatchAny:     r.MatchAny,
		Context:      ctx,
	}

	resp, err := c.Annotations.GetAnnotations(&req)
	if err != nil {
		return nil, fmt.Errorf("get annotations: %w", err)
	}

	return resp, nil
}

// getAnnotationTags returns annotation tags with optional filtering by tag name.
func getAnnotationTags(ctx context.Context, r annotationsReadRequest) (*annotations.GetAnnotationTagsOK, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	req := annotations.GetAnnotationTagsParams{
		Tag:     r.Tag,
		Limit:   r.TagLimit,
		Context: ctx,
	}

	resp, err := c.Annotations.GetAnnotationTags(&req)
	if err != nil {
		return nil, fmt.Errorf("get annotation tags: %w", err)
	}

	return resp, nil
}

func annotationsWrite(ctx context.Context, args AnnotationsWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("annotations_write: %w", err)
	}

	switch args.Operation {
	case "create":
		return createAnnotation(ctx, args)
	case "update":
		return updateAnnotation(ctx, args)
	case "delete":
		return deleteAnnotation(ctx, args)
	default:
		// Unreachable once validate() has passed; kept for defense in depth.
		return nil, fmt.Errorf("annotations_write: unknown operation %q", args.Operation)
	}
}

// createAnnotation sends a POST request to create a Grafana annotation.
// If Format is "graphite", it creates a Graphite-format annotation instead.
func createAnnotation(ctx context.Context, args AnnotationsWriteParams) (any, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	if args.Format == "graphite" {
		req := &models.PostGraphiteAnnotationsCmd{
			What: args.What,
			When: args.When,
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

	req := models.PostAnnotationsCmd{
		DashboardUID: args.DashboardUID,
		PanelID:      args.PanelID,
		Tags:         args.Tags,
		Text:         args.Text,
		Data:         args.Data,
	}
	if args.Time != nil {
		req.Time = *args.Time
	}
	if args.TimeEnd != nil {
		req.TimeEnd = *args.TimeEnd
	}

	resp, err := c.Annotations.PostAnnotationWithParams(
		annotations.NewPostAnnotationParamsWithContext(ctx).WithBody(&req),
	)
	if err != nil {
		return nil, fmt.Errorf("create annotation: %w", err)
	}

	return resp, nil
}

// updateAnnotation updates an annotation using PATCH semantics — only provided fields are modified.
func updateAnnotation(ctx context.Context, args AnnotationsWriteParams) (*annotations.PatchAnnotationOK, error) {
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
	return resp, nil
}

// deleteAnnotation deletes an annotation by ID.
func deleteAnnotation(ctx context.Context, args AnnotationsWriteParams) (string, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	id := strconv.FormatInt(args.ID, 10)

	_, err := c.Annotations.DeleteAnnotationByIDWithParams(
		annotations.NewDeleteAnnotationByIDParamsWithContext(ctx).WithAnnotationID(id),
	)
	if err != nil {
		return "", fmt.Errorf("delete annotation %s: %w", id, err)
	}

	return fmt.Sprintf("Annotation %s deleted successfully", id), nil
}
