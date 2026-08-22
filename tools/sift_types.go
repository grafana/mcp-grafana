package tools

import (
	"errors"
	"fmt"
	"time"
)

var siftReadOperations = []string{"list_investigations", "get_investigation", "get_analysis"}
var siftWriteOperations = []string{"find_error_pattern_logs", "find_slow_requests"}
var siftAllOperations = append(append([]string{}, siftReadOperations...), siftWriteOperations...)

// SiftReadParams is the param struct for sift_read.
type SiftReadParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=list_investigations,enum=get_investigation,enum=get_analysis,description=The operation to perform: 'list_investigations' to list recent Sift investigations\\, 'get_investigation' to retrieve one by UUID\\, or 'get_analysis' to retrieve a specific analysis from an investigation."`

	// list_investigations
	Limit int `json:"limit,omitempty" jsonschema:"default=10,description=Maximum number of investigations to return (for 'list_investigations')"`

	// get_investigation, get_analysis. Renamed from the pre-consolidation
	// get_sift_investigation tool's bare "id" to "investigationId" to match
	// get_sift_analysis's naming for the same concept, now that both live
	// under one operation param.
	InvestigationID string `json:"investigationId,omitempty" jsonschema:"description=The UUID of the investigation as a string\\, e.g. '02adab7c-bf5b-45f2-9459-d71a2c29e11b' (required for 'get_investigation' and 'get_analysis')"`

	// get_analysis
	AnalysisID string `json:"analysisId,omitempty" jsonschema:"description=The UUID of the specific analysis to retrieve (required for 'get_analysis')"`
}

func (p SiftReadParams) validate() error {
	return DelegateValidation(
		NewOperationValidator(p.Operation, siftReadOperations...).
			Require("get_investigation", StringField("investigationId", p.InvestigationID)).
			Require("get_analysis",
				StringField("investigationId", p.InvestigationID),
				StringField("analysisId", p.AnalysisID),
			).
			Check(),
		func() error {
			return UnknownOperationError(p.Operation, siftReadOperations...)
		},
	)
}

// SiftWriteParams is the param struct for sift_write.
//
// find_error_pattern_logs and find_slow_requests had IDENTICAL param
// shapes before consolidation (FindErrorPatternLogsParams and
// FindSlowRequestsParams were both exactly {Name, Labels, Start, End}) —
// unlike annotations_write's create/update, there's no per-operation field
// split needed here at all. Name and Labels are required for both
// operations unconditionally, so they're declared jsonschema-required
// directly rather than through OperationValidator.Require (which exists
// for fields that are required for SOME but not all of a tool's
// operations).
type SiftWriteParams struct {
	Operation string `json:"operation" jsonschema:"required,enum=find_error_pattern_logs,enum=find_slow_requests,description=The operation to perform: 'find_error_pattern_logs' searches Loki logs for elevated error patterns compared to the last day's average\\, or 'find_slow_requests' searches relevant Tempo datasources for slow requests. Both create a Sift investigation\\, wait for it to complete\\, and return the analysis."`

	Name   string            `json:"name" jsonschema:"required,description=The name of the investigation"`
	Labels map[string]string `json:"labels" jsonschema:"required,description=Labels to scope the analysis"`
	Start  time.Time         `json:"start,omitempty" jsonschema:"description=Start time for the investigation. Defaults to 30 minutes ago if not specified."`
	End    time.Time         `json:"end,omitempty" jsonschema:"description=End time for the investigation. Defaults to now if not specified."`
}

func (p SiftWriteParams) validate() error {
	return DelegateValidation(
		NewOperationValidator(p.Operation, siftWriteOperations...).Check(),
		func() error {
			// p.Operation isn't one of sift_write's own operations. A
			// schema-conformant call can't actually reach this — its
			// jsonschema enum only ever offers find_error_pattern_logs and
			// find_slow_requests — but if it's bypassed, or the caller
			// mistakenly sends a read-side operation, say so specifically
			// rather than lumping it in with a truly unknown value. This
			// must never fall through to accepting the operation:
			// sift_write does not gain "list_investigations"/
			// "get_investigation"/"get_analysis" capability just because
			// the sibling read tool recognizes them. See
			// AnnotationsWriteParams.validate in the git history (PR #1098)
			// for the same reasoning worked out in more detail, and
			// tools/operation_validation_test.go's
			// TestDelegation_SplitNameDisjointOperations for the pattern in
			// isolation.
			if readErr := NewOperationValidator(p.Operation, siftReadOperations...).Check(); !errors.Is(readErr, ErrUnknownOperation) {
				return fmt.Errorf("%q is a sift_read operation, not sift_write; call sift_read instead, or use one of: find_error_pattern_logs, find_slow_requests", p.Operation)
			}
			return UnknownOperationError(p.Operation, siftAllOperations...)
		},
	)
}
