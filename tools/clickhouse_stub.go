//go:build !clickhouse
// +build !clickhouse

// This is a stub file that provides minimal ClickHouse types for compilation.
// It will be replaced when PR #535 (ClickHouse tools) is merged.
// The actual implementation is in clickhouse.go from PR #535.

package tools

import (
	"context"
	"fmt"
)

// ClickHouseQueryParams stub - actual implementation in PR #535
type ClickHouseQueryParams struct {
	DatasourceUID string            `json:"datasourceUid"`
	Query         string            `json:"query"`
	Start         string            `json:"start,omitempty"`
	End           string            `json:"end,omitempty"`
	Variables     map[string]string `json:"variables,omitempty"`
}

// ClickHouseQueryResult stub - actual implementation in PR #535
type ClickHouseQueryResult struct {
	Columns        []string        `json:"columns"`
	Rows           [][]interface{} `json:"rows"`
	RowCount       int             `json:"rowCount"`
	ProcessedQuery string          `json:"processedQuery,omitempty"`
	Hints          []string        `json:"hints,omitempty"`
}

// queryClickHouse stub - returns error until PR #535 is merged
func queryClickHouse(ctx context.Context, args ClickHouseQueryParams) (*ClickHouseQueryResult, error) {
	return nil, fmt.Errorf("ClickHouse support requires PR #535 to be merged; use query_clickhouse directly with the query: %s", args.Query)
}
