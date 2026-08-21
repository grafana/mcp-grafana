//go:build unit
// +build unit

package tools

import (
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
