package tools

import (
	"fmt"
	"strings"
)

// OpField describes one parameter's presence for per-operation validation.
// Name is the JSON field name to use in error messages; Missing reports
// whether the caller omitted the value. "Missing" is type-specific (empty
// string, nil pointer, zero-length slice, etc.), so callers compute it
// themselves rather than the validator trying to infer it via reflection.
//
// Two convenience constructors, StringField and SliceField, cover the most
// common cases; for anything else construct the struct directly, e.g.
// OpField{Name: "rule_uid", Missing: p.RuleUID == nil}.
type OpField struct {
	Name    string
	Missing bool
}

// StringField is a convenience OpField constructor for a required string
// parameter, missing when empty.
func StringField(name, value string) OpField {
	return OpField{Name: name, Missing: value == ""}
}

// SliceField is a convenience OpField constructor for a required slice
// parameter, missing when empty.
func SliceField[T any](name string, value []T) OpField {
	return OpField{Name: name, Missing: len(value) == 0}
}

// exclusiveGroup is a set of fields that are either mutually exclusive (at
// most one set) or that require exactly one to be set. op, when non-empty,
// scopes the check to a single operation; an empty op applies the check
// regardless of the active operation.
type exclusiveGroup struct {
	op     string
	fields []OpField
}

// OperationValidator validates the "operation" discriminator parameter of a
// consolidated read (or read/write) tool: that the value is one of a known
// set, that fields required for the active operation are present, and that
// mutually-exclusive or exactly-one-of field groups are respected. It
// produces the same error message style used by hand-rolled `switch
// p.Operation { ... }` validation (see alerting_manage_rules), so callers see
// consistent errors regardless of which domain raised them.
//
// Typical usage, from a params struct's validate() method:
//
//	func (p ReadParams) validate() error {
//		return NewOperationValidator(p.Operation, "list", "get", "versions").
//			Require("get", StringField("rule_uid", p.RuleUID)).
//			Require("versions", StringField("rule_uid", p.RuleUID)).
//			MutuallyExclusiveFor("list", StringField("folder_uid", p.FolderUID), StringField("search_folder", p.SearchFolder)).
//			Validate()
//	}
//
// Business-rule validation beyond presence/exclusivity (enum membership,
// numeric ranges, cross-field parsing) is intentionally out of scope; keep
// that in the domain's own validate() alongside a call to Validate().
type OperationValidator struct {
	operation  string
	known      []string
	required   map[string][]OpField
	exclusive  []exclusiveGroup
	exactlyOne []exclusiveGroup
}

// NewOperationValidator starts validation for the given operation value
// against the set of operation values the tool supports. known should list
// every valid operation for this tool (the read-only tool's narrower set,
// or the read/write tool's fuller set).
func NewOperationValidator(operation string, known ...string) *OperationValidator {
	return &OperationValidator{
		operation: operation,
		known:     known,
		required:  make(map[string][]OpField),
	}
}

// Require registers fields that must be present when op is the active
// operation. Call once per operation that has required fields; multiple
// calls for the same op accumulate.
func (v *OperationValidator) Require(op string, fields ...OpField) *OperationValidator {
	v.required[op] = append(v.required[op], fields...)
	return v
}

// MutuallyExclusive registers a group of fields where at most one may be
// set, checked regardless of the active operation.
func (v *OperationValidator) MutuallyExclusive(fields ...OpField) *OperationValidator {
	v.exclusive = append(v.exclusive, exclusiveGroup{fields: fields})
	return v
}

// MutuallyExclusiveFor is like MutuallyExclusive but the check only applies
// when op is the active operation. Use this when the fields in the group are
// only meaningful for one operation (e.g. list-only filters).
func (v *OperationValidator) MutuallyExclusiveFor(op string, fields ...OpField) *OperationValidator {
	v.exclusive = append(v.exclusive, exclusiveGroup{op: op, fields: fields})
	return v
}

// ExactlyOne registers a group of fields where exactly one must be set,
// checked regardless of the active operation.
func (v *OperationValidator) ExactlyOne(fields ...OpField) *OperationValidator {
	v.exactlyOne = append(v.exactlyOne, exclusiveGroup{fields: fields})
	return v
}

// ExactlyOneFor is like ExactlyOne but the check only applies when op is the
// active operation.
func (v *OperationValidator) ExactlyOneFor(op string, fields ...OpField) *OperationValidator {
	v.exactlyOne = append(v.exactlyOne, exclusiveGroup{op: op, fields: fields})
	return v
}

// Validate runs the registered checks in order: unknown operation, mutually
// exclusive groups, exactly-one groups, then required fields for the active
// operation. It returns the first violation found.
func (v *OperationValidator) Validate() error {
	if !containsString(v.known, v.operation) {
		return fmt.Errorf("unknown operation %q, must be one of: %s", v.operation, strings.Join(v.known, ", "))
	}

	for _, g := range v.exclusive {
		if g.op != "" && g.op != v.operation {
			continue
		}
		if set := setFields(g.fields); len(set) > 1 {
			return fmt.Errorf("%s are mutually exclusive", strings.Join(set, " and "))
		}
	}

	for _, g := range v.exactlyOne {
		if g.op != "" && g.op != v.operation {
			continue
		}
		set := setFields(g.fields)
		switch len(set) {
		case 0:
			return fmt.Errorf("either %s must be set", strings.Join(fieldNames(g.fields), " or "))
		case 1:
			continue
		default:
			return fmt.Errorf("%s are mutually exclusive; pass exactly one", strings.Join(set, " and "))
		}
	}

	for _, f := range v.required[v.operation] {
		if f.Missing {
			return fmt.Errorf("%s is required for '%s' operation", f.Name, v.operation)
		}
	}

	return nil
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// setFields returns the names of the fields in the group that are present
// (not Missing), preserving group order.
func setFields(fields []OpField) []string {
	var set []string
	for _, f := range fields {
		if !f.Missing {
			set = append(set, f.Name)
		}
	}
	return set
}

func fieldNames(fields []OpField) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return names
}
