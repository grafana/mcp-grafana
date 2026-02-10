package tools

import (
	"fmt"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test for trailing whitespace in paths (bug fix)
func TestApplyJSONPath_TrailingWhitespace(t *testing.T) {
	t.Run("append with trailing space works", func(t *testing.T) {
		data := map[string]interface{}{
			"panels": []interface{}{"a", "b"},
		}
		// Path with trailing space - should be trimmed
		err := applyJSONPath(data, "$.panels/- ", "c", false)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "b", "c"}, data["panels"])
	})

	t.Run("path with leading and trailing whitespace", func(t *testing.T) {
		data := map[string]interface{}{
			"title": "old",
		}
		err := applyJSONPath(data, "  $.title  ", "new", false)
		require.NoError(t, err)
		assert.Equal(t, "new", data["title"])
	})
}

// Feature: dashboard-remove-array-element, Property 1: Array removal produces correct result
// Validates: Requirements 1.1, 1.2, 1.3
func TestProperty_ArrayRemovalProducesCorrectResult(t *testing.T) {
	f := func(size uint8) bool {
		// Ensure non-empty array (size 1-256)
		n := int(size) + 1

		// Build array of distinguishable elements
		arr := make([]interface{}, n)
		for j := 0; j < n; j++ {
			arr[j] = j
		}

		// Pick a random valid index
		idx := rand.Intn(n)

		// Build expected result
		expected := make([]interface{}, 0, n-1)
		expected = append(expected, arr[:idx]...)
		expected = append(expected, arr[idx+1:]...)

		// Build the map and segment
		current := map[string]interface{}{"items": copySlice(arr)}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: idx}

		// Execute
		err := removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// Verify
		result := current["items"].([]interface{})
		if len(result) != n-1 {
			return false
		}
		for k := range result {
			if result[k] != expected[k] {
				return false
			}
		}
		return true
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

func copySlice(s []interface{}) []interface{} {
	c := make([]interface{}, len(s))
	copy(c, s)
	return c
}

// Feature: dashboard-remove-array-element, Property 2: Out-of-bounds index returns error
// Validates: Requirements 1.4, 1.5
func TestProperty_OutOfBoundsIndexReturnsError(t *testing.T) {
	f := func(size uint8, offset uint8) bool {
		n := int(size) // array length 0-255

		// Build array
		arr := make([]interface{}, n)
		for j := 0; j < n; j++ {
			arr[j] = j
		}

		// Out-of-bounds index: n + offset (always >= n)
		idx := n + int(offset)

		// Build the map and segment
		original := copySlice(arr)
		current := map[string]interface{}{"items": copySlice(arr)}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: idx}

		// Execute
		err := removeAtSegment(current, segment)

		// Must return error
		if err == nil {
			return false
		}

		// Array must be unchanged
		result := current["items"].([]interface{})
		if len(result) != len(original) {
			return false
		}
		for k := range result {
			if result[k] != original[k] {
				return false
			}
		}
		return true
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

// Feature: dashboard-remove-array-element, Property 4: Object property removal is preserved
// Validates: Requirements 3.1
func TestProperty_ObjectPropertyRemovalPreserved(t *testing.T) {
	f := func(numKeys uint8) bool {
		// Build a map with 1 to 256 keys
		n := int(numKeys) + 1
		current := make(map[string]interface{})
		for j := 0; j < n; j++ {
			current[fmt.Sprintf("key_%d", j)] = j
		}

		// Pick a random key to remove
		targetIdx := rand.Intn(n)
		targetKey := fmt.Sprintf("key_%d", targetIdx)

		// Snapshot other keys
		otherKeys := make(map[string]interface{})
		for k, v := range current {
			if k != targetKey {
				otherKeys[k] = v
			}
		}

		// Execute removal (non-array segment)
		segment := JSONPathSegment{Key: targetKey, IsArray: false}
		err := removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// Target key must be absent
		if _, exists := current[targetKey]; exists {
			return false
		}

		// All other keys must be unchanged
		if len(current) != len(otherKeys) {
			return false
		}
		for k, v := range otherKeys {
			if current[k] != v {
				return false
			}
		}
		return true
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

// Unit tests for removeAtSegment edge cases
// Validates: Requirements 1.1, 1.4, 3.2
func TestRemoveAtSegment_EdgeCases(t *testing.T) {
	t.Run("remove first element", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b", "c"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"b", "c"}, current["items"])
	})

	t.Run("remove middle element", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b", "c"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 1}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "c"}, current["items"])
	})

	t.Run("remove last element", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b", "c"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 2}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "b"}, current["items"])
	})

	t.Run("remove from single-element array", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"only"}}
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		require.NoError(t, err)
		assert.Equal(t, []interface{}{}, current["items"])
	})

	t.Run("remove with append syntax returns error", func(t *testing.T) {
		current := map[string]interface{}{"items": []interface{}{"a", "b"}}
		segment := JSONPathSegment{Key: "items", IsAppend: true}
		err := removeAtSegment(current, segment)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append syntax")
	})

	t.Run("remove from non-array field returns error", func(t *testing.T) {
		current := map[string]interface{}{"title": "hello"}
		segment := JSONPathSegment{Key: "title", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not an array")
	})
}

// Feature: dashboard-remove-array-element, Property 3: Sequential removal shifts indices correctly
// Validates: Requirements 2.2
func TestProperty_SequentialRemovalShiftsIndices(t *testing.T) {
	f := func(size uint8) bool {
		// Ensure array of length >= 3 (need at least 3 elements for two sequential removals)
		n := int(size) + 3

		// Build array of distinguishable elements
		arr := make([]interface{}, n)
		for j := 0; j < n; j++ {
			arr[j] = j
		}

		// Build the map
		current := map[string]interface{}{"items": copySlice(arr)}

		// Remove index 0 (removes original element 0)
		segment := JSONPathSegment{Key: "items", IsArray: true, Index: 0}
		err := removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// After removal, index 0 should be what was originally at index 1
		result := current["items"].([]interface{})
		if result[0] != arr[1] {
			return false
		}

		// Remove index 0 again (removes what was originally element 1)
		err = removeAtSegment(current, segment)
		if err != nil {
			return false
		}

		// Now index 0 should be what was originally at index 2
		result = current["items"].([]interface{})
		if result[0] != arr[2] {
			return false
		}

		// Length should be n-2
		return len(result) == n-2
	}

	require.NoError(t, quick.Check(f, &quick.Config{MaxCount: 200}))
}

// Feature: dashboard-remove-array-element, Property 5: Nested array removal via full path
// Validates: Requirements 4.1
func TestApplyJSONPath_NestedArrayRemoval(t *testing.T) {
	t.Run("remove nested array element", func(t *testing.T) {
		// Build a structure mimicking a dashboard with panels containing targets
		data := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"title": "Panel 1",
					"targets": []interface{}{
						map[string]interface{}{"expr": "query_a"},
						map[string]interface{}{"expr": "query_b"},
						map[string]interface{}{"expr": "query_c"},
					},
				},
			},
		}

		// Remove targets[1] from panels[0]
		err := applyJSONPath(data, "$.panels[0].targets[1]", nil, true)
		require.NoError(t, err)

		// Verify outer structure is intact
		panels := data["panels"].([]interface{})
		require.Len(t, panels, 1)

		panel := panels[0].(map[string]interface{})
		assert.Equal(t, "Panel 1", panel["title"])

		// Verify inner array has the correct elements
		targets := panel["targets"].([]interface{})
		require.Len(t, targets, 2)
		assert.Equal(t, "query_a", targets[0].(map[string]interface{})["expr"])
		assert.Equal(t, "query_c", targets[1].(map[string]interface{})["expr"])
	})

	t.Run("remove from deeply nested path", func(t *testing.T) {
		data := map[string]interface{}{
			"panels": []interface{}{
				map[string]interface{}{
					"title":   "Panel 1",
					"targets": []interface{}{"t0", "t1", "t2"},
				},
				map[string]interface{}{
					"title":   "Panel 2",
					"targets": []interface{}{"t3", "t4"},
				},
			},
		}

		// Remove targets[0] from panels[1]
		err := applyJSONPath(data, "$.panels[1].targets[0]", nil, true)
		require.NoError(t, err)

		// Verify panels[0] is untouched
		panel0 := data["panels"].([]interface{})[0].(map[string]interface{})
		assert.Len(t, panel0["targets"].([]interface{}), 3)

		// Verify panels[1].targets has the correct element
		panel1 := data["panels"].([]interface{})[1].(map[string]interface{})
		targets := panel1["targets"].([]interface{})
		require.Len(t, targets, 1)
		assert.Equal(t, "t4", targets[0])
	})
}


// Unit tests for sortArrayRemovesDescending
// Validates: safe ordering of multiple array element removes
func TestSortArrayRemovesDescending(t *testing.T) {
	t.Run("single remove is unchanged", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[2]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[2]", result[0].Path)
	})

	t.Run("removes in descending order are unchanged", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[4]"},
			{Op: "remove", Path: "$.panels[2]"},
			{Op: "remove", Path: "$.panels[0]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[4]", result[0].Path)
		assert.Equal(t, "$.panels[2]", result[1].Path)
		assert.Equal(t, "$.panels[0]", result[2].Path)
	})

	t.Run("removes in ascending order are reordered", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[1]"},
			{Op: "remove", Path: "$.panels[3]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[3]", result[0].Path)
		assert.Equal(t, "$.panels[1]", result[1].Path)
	})

	t.Run("removes on different arrays are independent", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[1]"},
			{Op: "remove", Path: "$.annotations[3]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[1]", result[0].Path)
		assert.Equal(t, "$.annotations[3]", result[1].Path)
	})

	t.Run("nested array removes are sorted", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.panels[0].targets[1]"},
			{Op: "remove", Path: "$.panels[0].targets[3]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.panels[0].targets[3]", result[0].Path)
		assert.Equal(t, "$.panels[0].targets[1]", result[1].Path)
	})

	t.Run("mixed operations preserve non-remove order", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "replace", Path: "$.title", Value: "New Title"},
			{Op: "remove", Path: "$.panels[1]"},
			{Op: "add", Path: "$.panels/-", Value: map[string]interface{}{"id": 1}},
			{Op: "remove", Path: "$.panels[2]"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		// Non-remove ops stay in place
		assert.Equal(t, "replace", result[0].Op)
		assert.Equal(t, "$.title", result[0].Path)
		assert.Equal(t, "add", result[2].Op)
		// Remove ops are reordered: 2 before 1
		assert.Equal(t, "$.panels[2]", result[1].Path)
		assert.Equal(t, "$.panels[1]", result[3].Path)
	})

	t.Run("non-array removes are unchanged", func(t *testing.T) {
		ops := []PatchOperation{
			{Op: "remove", Path: "$.description"},
			{Op: "remove", Path: "$.tags"},
		}
		result, err := sortArrayRemovesDescending(ops)
		require.NoError(t, err)
		assert.Equal(t, "$.description", result[0].Path)
		assert.Equal(t, "$.tags", result[1].Path)
	})
}

func TestSortArrayRemovesDescending_SameIndexMultipleTimes(t *testing.T) {
	// Same index multiple times is rejected - likely an LLM mistake
	ops := []PatchOperation{
		{Op: "remove", Path: "$.panels[11]"},
		{Op: "remove", Path: "$.panels[11]"},
		{Op: "remove", Path: "$.panels[11]"},
	}
	_, err := sortArrayRemovesDescending(ops)
	require.Error(t, err, "Same index multiple times should be rejected")
	assert.Contains(t, err.Error(), "duplicate remove")
}
