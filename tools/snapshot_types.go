package tools

import (
	"errors"
	"fmt"
)

var snapshotReadOperations = []string{"list", "get"}
var snapshotWriteOperations = []string{"create", "delete"}
var snapshotAllOperations = append(append([]string{}, snapshotReadOperations...), snapshotWriteOperations...)

// SnapshotReadParams is the param struct for snapshots_read.
type SnapshotReadParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=list,enum=get,description=The operation to perform: 'list' dashboard snapshots with optional query and limit filters\\, or 'get' a specific snapshot's metadata and dashboard payload by key."`

	// list
	Query string `json:"query,omitempty" jsonschema:"description=Optional search query for snapshot name (for 'list')"`
	Limit *int   `json:"limit,omitempty" jsonschema:"description=Maximum number of snapshots to return\\, Grafana defaults to 1000 when omitted (for 'list')"`

	// get
	Key string `json:"key,omitempty" jsonschema:"description=Snapshot key to retrieve (required for 'get')"`
}

func (p SnapshotReadParams) validate() error {
	return DelegateValidation(
		NewOperationValidator(p.Operation, snapshotReadOperations...).
			Require("get", StringField("key", p.Key)).
			Check(),
		func() error {
			return UnknownOperationError(p.Operation, snapshotReadOperations...)
		},
	)
}

// SnapshotWriteParams is the param struct for snapshots_write.
type SnapshotWriteParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=create,enum=delete,description=The operation to perform: 'create' a snapshot from a full dashboard payload\\, or 'delete' an existing snapshot by key."`

	// create
	Dashboard map[string]any `json:"dashboard,omitempty" jsonschema:"description=Complete dashboard model to snapshot\\, as returned by Grafana dashboard APIs (required for 'create')"`
	Name      string         `json:"name,omitempty" jsonschema:"description=Optional snapshot name (for 'create')"`
	Expires   *int64         `json:"expires,omitempty" jsonschema:"description=Snapshot expiration in seconds\\, e.g. 3600 for 1 hour (for 'create')"`
	External  *bool          `json:"external,omitempty" jsonschema:"description=Store snapshot on external server. Requires key and deleteKey when true (for 'create')"`
	DeleteKey string         `json:"deleteKey,omitempty" jsonschema:"description=Secret key for deleting external snapshots\\, required when external is true (for 'create')"`

	// create and delete: create's own custom snapshot key (only meaningful
	// when external is true), and delete's target key, share this one
	// field since both are "the key of a snapshot" in context.
	Key string `json:"key,omitempty" jsonschema:"description=For 'create': custom snapshot key\\, required when external is true. For 'delete': the key of the snapshot to delete (required)."`
}

func (p SnapshotWriteParams) validate() error {
	return DelegateValidation(
		NewOperationValidator(p.Operation, snapshotWriteOperations...).
			Require("create", MapField("dashboard", p.Dashboard)).
			Require("delete", StringField("key", p.Key)).
			Check(),
		func() error {
			// p.Operation isn't one of snapshots_write's own operations. A
			// schema-conformant call can't actually reach this — its
			// jsonschema enum only ever offers create/delete — but if it's
			// bypassed, or the caller mistakenly sends a read-side
			// operation, say so specifically rather than lumping it in
			// with a truly unknown value. This must never fall through to
			// accepting the operation: see AnnotationsWriteParams.validate
			// in the git history (PR #1098) and
			// tools/operation_validation_test.go's
			// TestDelegation_SplitNameDisjointOperations for the pattern.
			if readErr := NewOperationValidator(p.Operation, snapshotReadOperations...).Check(); !errors.Is(readErr, ErrUnknownOperation) {
				return fmt.Errorf("%q is a snapshots_read operation, not snapshots_write; call snapshots_read instead, or use one of: create, delete", p.Operation)
			}
			return UnknownOperationError(p.Operation, snapshotAllOperations...)
		},
	)
}

func (p SnapshotWriteParams) validateExternal() error {
	if p.External == nil || !*p.External {
		return nil
	}
	if p.Key == "" {
		return fmt.Errorf("key is required when external is true")
	}
	if p.DeleteKey == "" {
		return fmt.Errorf("deleteKey is required when external is true")
	}
	return nil
}
