package tools

import (
	"errors"
	"fmt"

	"github.com/grafana/incident-go"
)

var incidentReadOperations = []string{"list", "get"}
var incidentWriteOperations = []string{"create", "update", "add_activity"}
var incidentAllOperations = append(append([]string{}, incidentReadOperations...), incidentWriteOperations...)

// IncidentReadParams is the param struct for incidents_read.
type IncidentReadParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=list,enum=get,description=The operation to perform: 'list' incidents with optional status/drill filters\\, or 'get' a single incident by ID."`

	// list
	Limit  int    `json:"limit,omitempty" jsonschema:"default=10,description=Maximum number of incidents to return (for 'list')"`
	Drill  bool   `json:"drill,omitempty" jsonschema:"description=Whether to include drill incidents (for 'list')"`
	Status string `json:"status,omitempty" jsonschema:"description=The status of the incidents to include. Valid values: 'active'\\, 'resolved' (for 'list')"`

	// get. Renamed from the pre-consolidation get_incident tool's bare "id"
	// to "incidentId" to match the identifier name used by incidents_write's
	// 'update' and 'add_activity' operations for the same concept.
	IncidentID string `json:"incidentId,omitempty" jsonschema:"description=The ID of the incident to retrieve (required for 'get')"`
}

func (p IncidentReadParams) validate() error {
	return DelegateValidation(
		NewOperationValidator(p.Operation, incidentReadOperations...).
			Require("get", StringField("incidentId", p.IncidentID)).
			Check(),
		func() error {
			return UnknownOperationError(p.Operation, incidentReadOperations...)
		},
	)
}

// IncidentWriteParams is the param struct for incidents_write.
type IncidentWriteParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=create,enum=update,enum=add_activity,description=The operation to perform: 'create' a new incident\\, 'update' an existing incident's status/severity/title\\, or 'add_activity' to append a note to an incident's timeline."`

	// create, update. For 'create' these are the initial values (title,
	// severity, and roomPrefix are required). For 'update' only the
	// provided fields are changed and at least one of status/severity/title
	// must be given; there is no roomPrefix for 'update' since a room can't
	// be changed after creation.
	Title    string `json:"title,omitempty" jsonschema:"description=Incident title. Required for 'create'; for 'update'\\, the new title (only provided fields are changed)."`
	Severity string `json:"severity,omitempty" jsonschema:"description=Incident severity\\, e.g. 'minor'\\, 'major'\\, 'critical'. Required for 'create'; for 'update'\\, the new severity."`
	Status   string `json:"status,omitempty" jsonschema:"description=Incident status\\, e.g. 'active' or 'resolved'. For 'create'\\, the initial status. For 'update'\\, the new status."`

	// create only
	RoomPrefix    string                   `json:"roomPrefix,omitempty" jsonschema:"description=The prefix of the room to create the incident in (required for 'create')"`
	IsDrill       bool                     `json:"isDrill,omitempty" jsonschema:"description=Whether the incident is a drill incident (for 'create')"`
	AttachCaption string                   `json:"attachCaption,omitempty" jsonschema:"description=The caption of the attachment (for 'create')"`
	AttachURL     string                   `json:"attachUrl,omitempty" jsonschema:"description=The URL of the attachment (for 'create')"`
	Labels        []incident.IncidentLabel `json:"labels,omitempty" jsonschema:"description=The labels to add to the incident (for 'create')"`

	// update, add_activity: the target incident.
	IncidentID string `json:"incidentId,omitempty" jsonschema:"description=The ID of the incident to update or add an activity to (required for 'update' and 'add_activity')"`

	// add_activity only
	Body      string `json:"body,omitempty" jsonschema:"description=The body of the activity note. URLs will be parsed and attached as context (required for 'add_activity')"`
	EventTime string `json:"eventTime,omitempty" jsonschema:"description=The time the activity occurred. Defaults to now if not provided (for 'add_activity')"`
}

func (p IncidentWriteParams) validate() error {
	return DelegateValidation(
		NewOperationValidator(p.Operation, incidentWriteOperations...).
			Require("create",
				StringField("title", p.Title),
				StringField("severity", p.Severity),
				StringField("roomPrefix", p.RoomPrefix),
			).
			Require("update", StringField("incidentId", p.IncidentID)).
			Require("add_activity",
				StringField("incidentId", p.IncidentID),
				StringField("body", p.Body),
			).
			Check(),
		func() error {
			// p.Operation isn't one of incidents_write's own operations. A
			// schema-conformant call can't actually reach this — its
			// jsonschema enum only ever offers create/update/add_activity —
			// but if it's bypassed, or the caller mistakenly sends a
			// read-side operation, say so specifically rather than lumping
			// it in with a truly unknown value. This must never fall
			// through to accepting the operation: see
			// AnnotationsWriteParams.validate in the git history (PR #1098)
			// and tools/operation_validation_test.go's
			// TestDelegation_SplitNameDisjointOperations for the pattern.
			if readErr := NewOperationValidator(p.Operation, incidentReadOperations...).Check(); !errors.Is(readErr, ErrUnknownOperation) {
				return fmt.Errorf("%q is an incidents_read operation, not incidents_write; call incidents_read instead, or use one of: create, update, add_activity", p.Operation)
			}
			return UnknownOperationError(p.Operation, incidentAllOperations...)
		},
	)
}
