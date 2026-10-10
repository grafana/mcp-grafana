package tools

import (
	"context"
	"fmt"
)

// resolvePanelDatasource preserves execution safeguards for both query paths.
func resolvePanelDatasource(ctx context.Context, source datasourceInfo, vars map[string]string, overrideUID, overrideType string) (datasourceInfo, error) {
	// Resolve datasource UID and type
	datasourceUID := source.UID
	datasourceType := source.Type

	// Apply explicit datasource overrides (highest priority)
	if overrideUID != "" {
		datasourceUID = overrideUID
		if overrideType != "" {
			datasourceType = overrideType
		}
	} else if isVariableReference(datasourceUID) {
		// Resolve variable reference only if no explicit override
		varName := extractVariableName(datasourceUID)
		if resolvedUID, ok := vars[varName]; ok {
			datasourceUID = resolvedUID
			// Reset type so it gets looked up from the resolved datasource
			datasourceType = ""
		} else {
			availableDS := getAvailableDatasourceUIDs(ctx, source.Type)
			return datasourceInfo{}, fmt.Errorf("datasource variable '%s' not found. Hint: Use 'datasourceUid' and 'datasourceType' to override. Available %s datasources: %v", datasourceUID, source.Type, availableDS)
		}
	}

	// Resolve the datasource type authoritatively from its UID whenever the
	// caller overrode the datasource, or when we don't yet have a type. The
	// datasource's real type — not a caller-supplied one — decides which
	// executor runs, because the executors are not equivalent: a Loki
	// datasource routed on a SQL/CloudWatch type would run through
	// executeSQLPanelQuery / executeCloudWatchPanelQuery, which query
	// /api/ds/query directly and so bypass the Loki label-matcher enforcement
	// that is applied only in the native Loki backend (loki_backend.go /
	// loki_enforce.go). A type declared in the panel JSON (no override) is
	// trusted as-is: it comes from the dashboard, not the caller.
	if datasourceUID != "" && (overrideUID != "" || datasourceType == "") {
		ds, lookupErr := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: datasourceUID})
		switch {
		case lookupErr == nil:
			// The datasource's real type wins over any caller-supplied type.
			datasourceType = ds.Type
		case datasourceType == "":
			// Cannot resolve the type and the caller gave nothing to fall back
			// on.
			availableDS := getAvailableDatasourceUIDs(ctx, "")
			return datasourceInfo{}, fmt.Errorf("could not resolve datasource '%s' (%v) and no datasourceType was provided. Hint: provide both 'datasourceUid' and 'datasourceType' to override. Available datasources: %v", datasourceUID, lookupErr, availableDS)
		case len(enforcedMatchers(ctx)) > 0 && normalizeDatasourceType(datasourceType) != "loki":
			// The datasource is unreadable, so the caller-supplied type is
			// unverified. With Loki label-matcher enforcement active, refuse
			// rather than route a possibly-Loki datasource onto the
			// /api/ds/query path, which bypasses enforcement. Fails closed,
			// mirroring the VictoriaLogs guard in lokiBackendForDatasource.
			return datasourceInfo{}, fmt.Errorf("refusing to run panel query for datasource '%s': Loki label-matcher enforcement is enabled and the datasource type could not be verified because the datasource is not readable; query Loki via query_loki_logs, or supply an accessible datasource", datasourceUID)
		default:
			// Unreadable datasource, but the caller supplied a fallback type and
			// enforcement (if any) is satisfied; keep the caller-supplied type.
		}
	}

	return datasourceInfo{UID: datasourceUID, Type: datasourceType}, nil
}
