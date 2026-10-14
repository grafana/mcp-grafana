//go:build unit || integration

package tools

import "context"

func preparationFixture(v2 bool, variable map[string]interface{}, query string) map[string]interface{} {
	ds := map[string]interface{}{"uid": "postgres-uid", "type": "grafana-postgresql-datasource"}
	if !v2 {
		return map[string]interface{}{
			"templating": map[string]interface{}{"list": []interface{}{variable}},
			"panels": []interface{}{map[string]interface{}{
				"id": float64(1), "title": "Preparation", "datasource": ds,
				"targets": []interface{}{map[string]interface{}{"refId": "A", "rawSql": query}},
			}},
		}
	}
	variableSpec := map[string]interface{}{}
	for k, v := range variable {
		variableSpec[k] = v
	}
	variableSpec["query"] = map[string]interface{}{
		"kind": "DataQuery", "group": ds["type"], "datasource": map[string]interface{}{"name": ds["uid"]},
		"spec": map[string]interface{}{"rawSql": variable["query"]},
	}
	kind := "QueryVariable"
	if variable["type"] == "custom" {
		kind = "CustomVariable"
	}
	return map[string]interface{}{
		"variables": []interface{}{map[string]interface{}{"kind": kind, "spec": variableSpec}},
		"elements": map[string]interface{}{"panel": map[string]interface{}{"kind": "Panel", "spec": map[string]interface{}{
			"id": float64(1), "title": "Preparation",
			"data": map[string]interface{}{"spec": map[string]interface{}{"queries": []interface{}{
				map[string]interface{}{"spec": map[string]interface{}{"refId": "A", "query": map[string]interface{}{
					"group": ds["type"], "datasource": map[string]interface{}{"name": ds["uid"]},
					"spec": map[string]interface{}{"rawSql": query},
				}}},
			}}},
		}}},
	}
}

func inspectPreparationFixture(ctx context.Context, db map[string]interface{}, v2 bool, args DashboardPanelQueriesParams) ([]panelQuery, error) {
	if v2 {
		rawArgs := args
		rawArgs.Variables = nil
		queries, err := getPanelQueriesV2(db, rawArgs)
		if err != nil {
			return nil, err
		}
		return prepareInspectedQueries(ctx, templatingV1FromV2(db), args, queries), nil
	}
	queries := extractPanelQueries(collectAllPanels(db)[0], nil, nil)
	return prepareInspectedQueries(ctx, db, args, queries), nil
}

func preparationPanelTarget(db map[string]interface{}, v2 bool) map[string]interface{} {
	if !v2 {
		return safeArray(collectAllPanels(db)[0], "targets")[0].(map[string]interface{})
	}
	panel := collectAllPanelsV2(db)[0]
	pq := safeArray(safeObject(safeObject(panel, "data"), "spec"), "queries")[0].(map[string]interface{})
	return safeObject(safeObject(safeObject(pq, "spec"), "query"), "spec")
}

// setPreparationDatasource changes the panel datasource without changing the
// datasource used by the fixture's variable option query.
func setPreparationDatasource(db map[string]interface{}, v2 bool, uid, dsType string) {
	if !v2 {
		collectAllPanels(db)[0]["datasource"] = map[string]interface{}{"uid": uid, "type": dsType}
		return
	}
	panel := collectAllPanelsV2(db)[0]
	pq := safeArray(safeObject(safeObject(panel, "data"), "spec"), "queries")[0].(map[string]interface{})
	query := safeObject(safeObject(pq, "spec"), "query")
	query["group"] = dsType
	query["datasource"] = map[string]interface{}{"name": uid}
}
