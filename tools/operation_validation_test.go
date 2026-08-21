//go:build unit
// +build unit

package tools

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperationValidator_UnknownOperation(t *testing.T) {
	err := NewOperationValidator("bogus", "list", "get").Validate()
	require.Error(t, err)
	assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, get`)
}

func TestOperationValidator_RequiredField(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		err := NewOperationValidator("get", "list", "get").
			Require("get", StringField("rule_uid", "")).
			Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "rule_uid is required for 'get' operation")
	})

	t.Run("present", func(t *testing.T) {
		err := NewOperationValidator("get", "list", "get").
			Require("get", StringField("rule_uid", "abc")).
			Validate()
		require.NoError(t, err)
	})

	t.Run("only checked for its own operation", func(t *testing.T) {
		err := NewOperationValidator("list", "list", "get").
			Require("get", StringField("rule_uid", "")).
			Validate()
		require.NoError(t, err)
	})

	t.Run("multiple required fields, accumulated across calls", func(t *testing.T) {
		v := NewOperationValidator("create", "create").
			Require("create", StringField("title", "t")).
			Require("create", StringField("folder_uid", ""))
		err := v.Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "folder_uid is required for 'create' operation")
	})

	t.Run("slice field", func(t *testing.T) {
		err := NewOperationValidator("user_roles", "user_roles").
			Require("user_roles", SliceField("userIds", []int64{})).
			Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "userIds is required for 'user_roles' operation")

		err = NewOperationValidator("user_roles", "user_roles").
			Require("user_roles", SliceField("userIds", []int64{1})).
			Validate()
		require.NoError(t, err)
	})
}

func TestOperationValidator_MutuallyExclusive(t *testing.T) {
	t.Run("both set", func(t *testing.T) {
		err := NewOperationValidator("list", "list").
			MutuallyExclusive(StringField("folder_uid", "a"), StringField("search_folder", "b")).
			Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "folder_uid and search_folder are mutually exclusive")
	})

	t.Run("one set", func(t *testing.T) {
		err := NewOperationValidator("list", "list").
			MutuallyExclusive(StringField("folder_uid", "a"), StringField("search_folder", "")).
			Validate()
		require.NoError(t, err)
	})

	t.Run("neither set", func(t *testing.T) {
		err := NewOperationValidator("list", "list").
			MutuallyExclusive(StringField("folder_uid", ""), StringField("search_folder", "")).
			Validate()
		require.NoError(t, err)
	})

	t.Run("scoped to one operation", func(t *testing.T) {
		err := NewOperationValidator("get", "list", "get").
			MutuallyExclusiveFor("list", StringField("folder_uid", "a"), StringField("search_folder", "b")).
			Validate()
		require.NoError(t, err, "the group is scoped to 'list' and shouldn't fire for 'get'")
	})
}

func TestOperationValidator_ExactlyOne(t *testing.T) {
	t.Run("both set", func(t *testing.T) {
		err := NewOperationValidator("x", "x").
			ExactlyOne(StringField("dashboardUid", "a"), StringField("provisioningPreview", "b")).
			Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "dashboardUid and provisioningPreview are mutually exclusive; pass exactly one")
	})

	t.Run("neither set", func(t *testing.T) {
		err := NewOperationValidator("x", "x").
			ExactlyOne(StringField("dashboardUid", ""), StringField("provisioningPreview", "")).
			Validate()
		require.Error(t, err)
		assert.EqualError(t, err, "either dashboardUid or provisioningPreview must be set")
	})

	t.Run("exactly one set", func(t *testing.T) {
		err := NewOperationValidator("x", "x").
			ExactlyOne(StringField("dashboardUid", "a"), StringField("provisioningPreview", "")).
			Validate()
		require.NoError(t, err)
	})

	t.Run("scoped to one operation", func(t *testing.T) {
		err := NewOperationValidator("other", "x", "other").
			ExactlyOneFor("x", StringField("a", ""), StringField("b", "")).
			Validate()
		require.NoError(t, err, "the group is scoped to 'x' and shouldn't fire for 'other'")
	})
}

func TestOperationValidator_CheckOrder(t *testing.T) {
	// Unknown operation is reported before any exclusivity or required-field
	// checks even run, since those checks are meaningless for an operation
	// the tool doesn't support.
	err := NewOperationValidator("bogus", "list").
		Require("bogus", StringField("x", "")).
		Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown operation")
}

func TestOperationValidator_CheckVsValidate(t *testing.T) {
	t.Run("Check returns the bare sentinel", func(t *testing.T) {
		err := NewOperationValidator("bogus", "list", "get").Check()
		assert.ErrorIs(t, err, ErrUnknownOperation)
		assert.Equal(t, ErrUnknownOperation, err, "Check should return the sentinel itself, not a wrapped copy")
	})

	t.Run("Validate formats it", func(t *testing.T) {
		err := NewOperationValidator("bogus", "list", "get").Validate()
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrUnknownOperation, "Validate's formatted message is a plain error, not a wrapper around the sentinel")
		assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, get`)
	})

	t.Run("a recognized operation's required-field violation is identical via either method", func(t *testing.T) {
		checkErr := NewOperationValidator("get", "list", "get").Require("get", StringField("rule_uid", "")).Check()
		validateErr := NewOperationValidator("get", "list", "get").Require("get", StringField("rule_uid", "")).Validate()
		assert.Equal(t, checkErr, validateErr)
		assert.NotErrorIs(t, checkErr, ErrUnknownOperation, "a required-field violation on a known operation is not the unknown-operation sentinel")
	})
}

func TestDelegateValidation(t *testing.T) {
	t.Run("falls through only on the sentinel", func(t *testing.T) {
		called := false
		err := DelegateValidation(ErrUnknownOperation, func() error {
			called = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("a non-sentinel error short-circuits without calling fallback", func(t *testing.T) {
		called := false
		want := errors.New("required field missing")
		err := DelegateValidation(want, func() error {
			called = true
			return nil
		})
		assert.Equal(t, want, err)
		assert.False(t, called, "fallback must not run when the primary already owns the operation")
	})

	t.Run("nil (valid, owned by primary) short-circuits too", func(t *testing.T) {
		called := false
		err := DelegateValidation(nil, func() error {
			called = true
			return errors.New("should not run")
		})
		require.NoError(t, err)
		assert.False(t, called)
	})
}

func TestDelegateDispatch(t *testing.T) {
	t.Run("falls through only on the sentinel", func(t *testing.T) {
		result, err := DelegateDispatch("", ErrUnknownOperation, func() (string, error) {
			return "fallback result", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "fallback result", result)
	})

	t.Run("a successful primary result short-circuits without calling fallback", func(t *testing.T) {
		called := false
		result, err := DelegateDispatch("primary result", nil, func() (string, error) {
			called = true
			return "", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "primary result", result)
		assert.False(t, called)
	})
}

// The two subtests below are reference implementations of the read/write
// delegation idiom for the two shapes a consolidated domain can take.
// Copy whichever matches your domain's shape.

// TestDelegation_SingleNameDualSchema is the shape alerting_manage_rules and
// the agento11y_manage_* tools use: ONE tool name, registered with either a
// narrower read-only schema or a fuller read+write schema depending on
// enableWriteTools. Because only one schema is ever live at a time, the
// read+write variant's validate() and dispatch must themselves handle every
// read operation too — there is no separate read tool to fall back on. Read
// case bodies belong in exactly one place (the read variant); the
// read+write variant delegates to it instead of re-implementing them.
func TestDelegation_SingleNameDualSchema(t *testing.T) {
	readOps := []string{"list", "get"}
	writeOps := []string{"create", "delete"}
	allOps := append(append([]string{}, readOps...), writeOps...)

	// readDispatch is the one and only place "list"/"get" are implemented.
	readDispatch := func(op string) (string, error) {
		if err := NewOperationValidator(op, readOps...).Check(); err != nil {
			return "", err // ErrUnknownOperation for anything not list/get
		}
		return "read result for " + op, nil
	}

	// readWriteDispatch is what the read+write variant's handler calls. It
	// tries readDispatch first and only falls through to its own switch
	// when readDispatch reports the operation isn't one it owns.
	readWriteDispatch := func(op string) (string, error) {
		result, err := readDispatch(op)
		return DelegateDispatch(result, err, func() (string, error) {
			switch op {
			case "create":
				return "created", nil
			case "delete":
				return "deleted", nil
			default:
				return "", UnknownOperationError(op, allOps...)
			}
		})
	}

	t.Run("a read operation reaches the read implementation, not a duplicate", func(t *testing.T) {
		result, err := readWriteDispatch("get")
		require.NoError(t, err)
		assert.Equal(t, "read result for get", result)
	})

	t.Run("a write operation reaches the write switch", func(t *testing.T) {
		result, err := readWriteDispatch("create")
		require.NoError(t, err)
		assert.Equal(t, "created", result)
	})

	t.Run("an operation neither side owns gets the combined error", func(t *testing.T) {
		_, err := readWriteDispatch("bogus")
		require.Error(t, err)
		assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, get, create, delete`)
	})
}

// TestDelegation_SplitNameDisjointOperations is the shape the new
// consolidated tools use (e.g. admin_read, and annotations_read /
// annotations_write): two DIFFERENT tool names, each advertising its own
// disjoint operation set, both registered at once when writes are enabled.
// There is no dispatch-level duplication to remove here — annotations_write
// never receives "list" or "tags" from a schema-conformant caller, because
// its own jsonschema enum doesn't list them. Delegation still earns its
// keep at the VALIDATION layer: it lets the write tool recognize "you
// passed one of the read tool's operations, not mine" and say so, rather
// than lumping it in with genuinely unknown operations — but it must NOT
// treat a sibling's operation as valid for itself, or the write tool would
// silently gain read capability it doesn't advertise.
func TestDelegation_SplitNameDisjointOperations(t *testing.T) {
	readOps := []string{"list", "tags"}
	writeOps := []string{"create", "update", "delete"}
	allOps := append(append([]string{}, readOps...), writeOps...)

	validateWrite := func(op string) error {
		err := NewOperationValidator(op, writeOps...).Check()
		if !errors.Is(err, ErrUnknownOperation) {
			return err // one of ours: nil, or a required-field violation
		}
		// Not one of ours. Check whether it belongs to the sibling read
		// tool purely to give a more specific error - this must return an
		// error either way, never nil, or the write tool would silently
		// accept a read operation it doesn't implement.
		if readErr := NewOperationValidator(op, readOps...).Check(); !errors.Is(readErr, ErrUnknownOperation) {
			return fmt.Errorf("%q is an operation of the read tool, not this write tool", op)
		}
		return UnknownOperationError(op, allOps...)
	}

	t.Run("a write operation validates normally", func(t *testing.T) {
		require.NoError(t, validateWrite("create"))
	})

	t.Run("a sibling read operation is rejected, not silently accepted", func(t *testing.T) {
		err := validateWrite("list")
		require.Error(t, err, "must never return nil for an operation this tool doesn't implement")
		assert.Contains(t, err.Error(), "read tool, not this write tool")
	})

	t.Run("a genuinely unknown operation gets the combined error", func(t *testing.T) {
		err := validateWrite("bogus")
		require.Error(t, err)
		assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, tags, create, update, delete`)
	})
}
