//go:build unit

package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appendCustomAllFixtureVariable(db map[string]interface{}, v2 bool, variable map[string]interface{}) {
	if v2 {
		db["variables"] = append(safeArray(db, "variables"), map[string]interface{}{"kind": "CustomVariable", "spec": variable})
	} else {
		templating := safeObject(db, "templating")
		templating["list"] = append(safeArray(templating, "list"), variable)
	}
}

func TestDashboardNestedCustomAll(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, reference := range []string{"$choice", "${choice:sqlstring}", "[[choice:sqlstring]]"} {
			for _, override := range []string{"", "O'Reilly"} {
				t.Run(fmt.Sprintf("v2=%t/%s/override=%s", v2, reference, override), func(t *testing.T) {
					db := preparationFixture(v2, map[string]interface{}{
						"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": "${middle:sqlstring}",
					}, "SELECT "+reference)
					appendCustomAllFixtureVariable(db, v2, map[string]interface{}{
						"name": "middle", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": "[[leaf:sqlstring]]",
					})
					appendCustomAllFixtureVariable(db, v2, map[string]interface{}{
						"name": "leaf", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": "'east','west'",
					})
					overrides := map[string]string{}
					want := "SELECT 'east','west'"
					if override != "" {
						overrides["leaf"] = override
						want = "SELECT 'O''Reilly'"
					}
					// The same saved dashboard must yield the same expansion each time.
					for range 20 {
						queries, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: overrides})
						require.NoError(t, err)
						require.Len(t, queries, 1)
						require.Empty(t, queries[0].Warnings)
						assert.Equal(t, want, queries[0].ProcessedQuery)
					}
				})
			}
		}
	}
}

func TestDashboardCustomAllCycles(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, reference := range []string{"${choice}", "${middle}"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, reference), func(t *testing.T) {
				db := preparationFixture(v2, map[string]interface{}{
					"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": reference,
				}, "SELECT ${choice}")
				appendCustomAllFixtureVariable(db, v2, map[string]interface{}{
					"name": "middle", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": "${choice}",
				})
				queries, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
				require.NoError(t, err)
				require.Len(t, queries, 1)
				assert.Empty(t, queries[0].ProcessedQuery)
				assert.Contains(t, strings.Join(queries[0].Warnings, " "), "cyclic dependency")
				_, err = runSinglePanelQuery(t.Context(), singlePanelQueryParams{DB: db, IsV2: v2, PanelID: 1})
				require.ErrorContains(t, err, "cyclic dependency")
			})
		}
	}
}

func TestDashboardNestedCustomAllPreservesSelectedData(t *testing.T) {
	db := preparationFixture(false, map[string]interface{}{
		"name": "choice", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": "${saved:sqlstring}",
	}, "SELECT ${choice}")
	appendCustomAllFixtureVariable(db, false, map[string]interface{}{
		"name": "saved", "type": "custom", "current": map[string]interface{}{"value": "$other $__from"},
	})
	appendCustomAllFixtureVariable(db, false, map[string]interface{}{
		"name": "other", "type": "custom", "current": map[string]interface{}{"value": "$__all"}, "allValue": "'must remain unused'",
	})
	prepared, err := prepareDashboardQuery(t.Context(), db, "SELECT ${choice}", nil,
		datasourceInfo{UID: "postgres-uid", Type: "postgres"}, nil, "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", "", "", nil)
	require.NoError(t, err)
	require.Empty(t, prepared.Warnings)
	assert.Equal(t, "SELECT '$other $__from'", prepared.Query)
}

func TestDashboardSQLDollarAmountsAreNotRequiredVariables(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprint(v2), func(t *testing.T) {
			db := preparationFixture(v2, map[string]interface{}{"name": "unused", "type": "custom"}, "SELECT '$100' AS amount")
			queries, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
			require.NoError(t, err)
			require.Len(t, queries, 1)
			require.Empty(t, queries[0].Warnings)
			assert.Empty(t, queries[0].RequiredVariables)
			assert.Equal(t, "SELECT '$100' AS amount", queries[0].ProcessedQuery)

			// Embedded dollar signs in legacy datasource UIDs are also literal.
			setPreparationDatasource(db, v2, "literal-$100", "postgres")
			queries, err = inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
			require.NoError(t, err)
			require.Empty(t, queries[0].Warnings)
			assert.Empty(t, queries[0].RequiredVariables)
			assert.Equal(t, "literal-$100", queries[0].Datasource.UID)
			assert.Equal(t, "SELECT '$100' AS amount", queries[0].ProcessedQuery)

			// Explicit numeric overrides still name a real dashboard variable.
			queries, err = inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{"100": "east"}})
			require.NoError(t, err)
			require.Len(t, queries[0].RequiredVariables, 1)
			assert.Equal(t, "100", queries[0].RequiredVariables[0].Name)
			assert.Equal(t, "SELECT 'east' AS amount", queries[0].ProcessedQuery)

			// A target field is an identifier/template, not literal SQL text.
			preparationPanelTarget(db, v2)["location"] = "${100}"
			queries, err = inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
			require.NoError(t, err)
			require.Len(t, queries[0].RequiredVariables, 1)
			assert.Equal(t, "100", queries[0].RequiredVariables[0].Name)
		})
	}
}

func TestDashboardMissingNumericPlaceholdersRemainRequired(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		for _, reference := range []string{"${100}", "[[100]]", "${100:sqlstring}", "${123foo}", "$123foo"} {
			t.Run(fmt.Sprintf("v2=%t/%s", v2, reference), func(t *testing.T) {
				db := preparationFixture(v2, map[string]interface{}{"name": "unused", "type": "custom"}, "SELECT "+reference)
				queries, err := inspectPreparationFixture(t.Context(), db, v2, DashboardPanelQueriesParams{Variables: map[string]string{}})
				require.NoError(t, err)
				require.Len(t, queries, 1)
				require.Len(t, queries[0].RequiredVariables, 1)
				assert.Equal(t, "SELECT "+reference, queries[0].ProcessedQuery)
			})
		}
	}
}
