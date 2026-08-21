package tools

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnknownOperation is returned by OperationValidator.Check (and should be
// returned by any hand-rolled per-operation validator or dispatcher) when an
// operation value isn't one the checker owns. It's the shared sentinel
// behind the read/write delegation pattern: a tool that validates or
// dispatches a subset of a domain's operations returns this bare sentinel
// for operations outside its subset, so a caller composing two such
// checkers can tell "not mine" apart from "mine, and invalid" using
// errors.Is, and only then fall through to the next checker. See
// DelegateValidation and DelegateDispatch below, and OperationValidator.Check
// vs Validate.
var ErrUnknownOperation = errors.New("unknown operation")

// UnknownOperationError formats the final, user-facing "unknown operation"
// error once every checker in a delegation chain has rejected it. known
// should list every operation across the whole chain (e.g. a read
// validator's operations plus a write validator's), not just the last
// checker's subset, so the message tells the caller the full menu.
func UnknownOperationError(operation string, known ...string) error {
	return fmt.Errorf("unknown operation %q, must be one of: %s", operation, strings.Join(known, ", "))
}

// DelegateValidation is the read/write delegation idiom for a validate()
// method: try the primary checker's result first; if it reports
// ErrUnknownOperation (the operation isn't one the primary checker owns),
// run fallback and return its result instead. A non-nil, non-sentinel error
// from the primary (e.g. a required-field violation) is returned as-is,
// without ever running fallback — an operation the primary owns is not
// retried elsewhere just because it was invalid.
//
// Typical use, from a read/write params struct's validate():
//
//	func (p WriteParams) validate() error {
//		return DelegateValidation(p.readRequest().validate(p.Operation), func() error {
//			return NewOperationValidator(p.Operation, "create", "update", "delete").
//				Require("create", ...).
//				Validate()
//		})
//	}
func DelegateValidation(err error, fallback func() error) error {
	if errors.Is(err, ErrUnknownOperation) {
		return fallback()
	}
	return err
}

// DelegateDispatch is DelegateValidation's counterpart for a handler's
// dispatch: try the primary dispatcher's (result, err) first; if err is
// ErrUnknownOperation, run fallback and return its result instead. Any
// other result or error from the primary — including a successful one — is
// returned as-is.
//
// Typical use, from a read/write tool's handler:
//
//	func writeHandler(ctx context.Context, args WriteParams) (any, error) {
//		result, err := readDispatch(ctx, args.readRequest())
//		return DelegateDispatch(result, err, func() (any, error) {
//			switch args.Operation { ... }
//		})
//	}
func DelegateDispatch[R any](result R, err error, fallback func() (R, error)) (R, error) {
	if errors.Is(err, ErrUnknownOperation) {
		return fallback()
	}
	return result, err
}

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

// Check runs the same logic as Validate, but reports an unrecognized
// operation as the bare ErrUnknownOperation sentinel instead of a formatted
// message. Use Check (with DelegateValidation) when this validator is one
// link in a read/write delegation chain and a later link needs to detect
// "not mine" via errors.Is before producing the final, combined error
// message; use Validate when this validator is the final word (a read-only
// tool, or the last checker in a chain).
func (v *OperationValidator) Check() error {
	if !containsString(v.known, v.operation) {
		return ErrUnknownOperation
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

// Validate is Check, with an unrecognized operation formatted into the
// final user-facing message (via UnknownOperationError) instead of the bare
// ErrUnknownOperation sentinel. See Check's doc comment for when to use
// which.
func (v *OperationValidator) Validate() error {
	err := v.Check()
	if errors.Is(err, ErrUnknownOperation) {
		return UnknownOperationError(v.operation, v.known...)
	}
	return err
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
