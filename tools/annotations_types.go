package tools

import (
	"errors"
	"fmt"
)

var annotationsReadOperations = []string{"list", "tags"}
var annotationsWriteOperations = []string{"create", "update", "delete"}
var annotationsAllOperations = append(append([]string{}, annotationsReadOperations...), annotationsWriteOperations...)

// annotationsReadRequest carries the data fields for annotations_read's
// operations (list, tags). It's a plain data carrier with no Operation
// field of its own: annotations_read and annotations_write each declare
// their own Operation field (with their own jsonschema enum), and pass the
// value in explicitly to validate/dispatch against this shared shape. This
// mirrors agento11y_manage_evaluators's agento11yEvaluatorReadRequest.
type annotationsReadRequest struct {
	// list
	From         *int64   `json:"from,omitempty" jsonschema:"description=Epoch ms start time (for 'list')"`
	To           *int64   `json:"to,omitempty" jsonschema:"description=Epoch ms end time (for 'list')"`
	Limit        *int64   `json:"limit,omitempty" jsonschema:"description=Max results\\, default 100 (for 'list')"`
	AlertUID     *string  `json:"alertUid,omitempty" jsonschema:"description=Filter by alert UID (for 'list')"`
	DashboardUID *string  `json:"dashboardUid,omitempty" jsonschema:"description=Filter by dashboard UID (for 'list')"`
	PanelID      *int64   `json:"panelId,omitempty" jsonschema:"description=Filter by panel ID (for 'list')"`
	UserID       *int64   `json:"userId,omitempty" jsonschema:"description=Filter by creator user ID (for 'list')"`
	Type         *string  `json:"type,omitempty" jsonschema:"description=Filter by type: annotation or alert (for 'list')"`
	Tags         []string `json:"tags,omitempty" jsonschema:"description=Filter by tags. Multiple tags allowed; use matchAny to control AND/OR logic (for 'list')"`
	MatchAny     *bool    `json:"matchAny,omitempty" jsonschema:"description=If true\\, match any tag (OR). If false\\, match all tags (AND). Default: false (for 'list')"`

	// tags
	Tag      *string `json:"tag,omitempty" jsonschema:"description=Filter by tag name (for 'tags')"`
	TagLimit *string `json:"tagLimit,omitempty" jsonschema:"description=Max results\\, default 100 (for 'tags')"`
}

// validate checks operation against annotations_read's own operations
// (list, tags), returning the bare ErrUnknownOperation sentinel for
// anything else so a caller composing this with another checker (see
// AnnotationsWriteParams.validate) can tell "not mine" apart from "mine,
// and invalid" via errors.Is. Neither list nor tags has a required field
// today, but the OperationValidator is used anyway (rather than returning
// nil directly) so adding one later doesn't change this method's shape.
func (r annotationsReadRequest) validate(operation string) error {
	return NewOperationValidator(operation, annotationsReadOperations...).Check()
}

// AnnotationsReadParams is the param struct for annotations_read.
type AnnotationsReadParams struct {
	annotationsReadRequest

	Operation string `json:"operation" jsonschema:"required,enum=list,enum=tags,description=The operation to perform: 'list' to fetch annotations filtered by time range\\, dashboard\\, panel\\, alert\\, user\\, type\\, or tags\\, or 'tags' to list annotation tag names with optional filtering."`
}

func (p AnnotationsReadParams) validate() error {
	return DelegateValidation(p.annotationsReadRequest.validate(p.Operation), func() error {
		return UnknownOperationError(p.Operation, annotationsReadOperations...)
	})
}

// AnnotationsWriteParams is the param struct for annotations_write.
//
// It does NOT embed annotationsReadRequest: none of its fields are actually
// shared with the read side once you account for role (e.g. a filter
// DashboardUID on 'list' is not the same thing as a target DashboardUID on
// 'create', even though the concept has the same name — see
// alerting_manage_rules's FolderUID for the established precedent of
// keeping filter-role and target-role fields declared separately even when
// they'd otherwise look identical).
type AnnotationsWriteParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=create,enum=update,enum=delete,description=The operation to perform: 'create' a new annotation (standard or Graphite format)\\, 'update' fields on an existing annotation by ID (only provided fields are modified)\\, or 'delete' an annotation by ID."`

	// create only
	DashboardUID string `json:"dashboardUid,omitempty" jsonschema:"description=Dashboard UID to scope the new annotation to. Omit for an organization-wide annotation (for 'create')"`
	PanelID      int64  `json:"panelId,omitempty" jsonschema:"description=Panel ID to scope the new annotation to (for 'create')"`
	Format       string `json:"format,omitempty" jsonschema:"enum=graphite,description=Set to 'graphite' to create a Graphite-format annotation instead of a standard one (for 'create')"`
	What         string `json:"what,omitempty" jsonschema:"description=Annotation text for Graphite format\\, required when format is 'graphite' (for 'create')"`
	When         int64  `json:"when,omitempty" jsonschema:"description=Epoch ms timestamp for Graphite format (for 'create')"`
	GraphiteData string `json:"graphiteData,omitempty" jsonschema:"description=Optional string payload for Graphite format (for 'create')"`

	// update and delete
	ID int64 `json:"id,omitempty" jsonschema:"description=Annotation ID (required for 'update' and 'delete')"`

	// create and update. Pointers so 'update' can distinguish "omitted,
	// leave unchanged" (PATCH semantics) from an explicit zero/empty value;
	// 'create' uses the same nil-means-absent convention.
	Text    *string        `json:"text,omitempty" jsonschema:"description=Annotation text. For 'create'\\, required unless format is 'graphite'. For 'update'\\, omit to leave unchanged (for 'create' and 'update')"`
	Time    *int64         `json:"time,omitempty" jsonschema:"description=Start time epoch ms (for 'create' and 'update')"`
	TimeEnd *int64         `json:"timeEnd,omitempty" jsonschema:"description=End time epoch ms (for 'create' and 'update')"`
	Tags    []string       `json:"tags,omitempty" jsonschema:"description=Tags. For 'create'\\, tags to attach; for 'update'\\, replaces the existing tags entirely if provided (for 'create' and 'update')"`
	Data    map[string]any `json:"data,omitempty" jsonschema:"description=Optional JSON payload (for 'create' and 'update')"`
}

func (p AnnotationsWriteParams) validate() error {
	return DelegateValidation(
		NewOperationValidator(p.Operation, annotationsWriteOperations...).
			Require("create",
				OpField{Name: "text", Missing: p.Format != "graphite" && (p.Text == nil || *p.Text == "")},
				OpField{Name: "what", Missing: p.Format == "graphite" && p.What == ""},
			).
			Require("update", OpField{Name: "id", Missing: p.ID == 0}).
			Require("delete", OpField{Name: "id", Missing: p.ID == 0}).
			Check(),
		func() error {
			// p.Operation isn't one of annotations_write's own operations.
			// A schema-conformant call can't actually reach this — the
			// jsonschema enum above only ever offers create/update/delete —
			// but if it's bypassed, or the caller mistakenly sends a
			// read-side operation, say so specifically rather than lumping
			// it in with a truly unknown value. This must never fall
			// through to accepting the operation: annotations_write does
			// not gain "list"/"tags" capability just because the sibling
			// read tool recognizes them.
			if readErr := (annotationsReadRequest{}).validate(p.Operation); !errors.Is(readErr, ErrUnknownOperation) {
				return fmt.Errorf("%q is an annotations_read operation, not annotations_write; call annotations_read instead, or use one of: create, update, delete", p.Operation)
			}
			return UnknownOperationError(p.Operation, annotationsAllOperations...)
		},
	)
}
