package sql

import (
	"context"
	"slices"
	"strconv"
	"time"

	client "github.com/grafana/mcp-grafana/pkg/grafana"
)

// SQLDatabase represents the interface for metadata queries
type SQLDatabase interface {
	// Type returns the database engine type (e.g., "mysql", "mssql").
	Type() string

	// GetDatabaseQuery builds a query to retrieve database names, excluding system/internal databases.
	GetDatabaseQuery() string

	// GetTablesQuery builds a query to retrieve table names of a database excluding internal tables
	// dbName is optional. It uses default database when empty
	GetTablesQuery(dbName string) string

	// GetSchemaQuery builds a query to retrieve the schema for a specific table.
	// The dbName is optional and can be used to qualify the table.
	GetSchemaQuery(tableName string, dbName string) string

	// QueryWithLimit wraps the provided query to enforce a row limit.
	// It returns the modified query and a boolean indicating if wrapping was successful.
	QueryWithLimit(query string, limit uint) (string, bool)

	// GetInfoQuery builds a query to retrieve database version and system information.
	GetInfoQuery() string
}

type sqlEngine struct {
	grafanaClient *client.GrafanaClient
}

type BuildQueryArgs struct {
	RefID         string
	DatasourceUId string
	Query         string
	DB            *SQLDatabase
	IntervalMs    uint //optional : not applicable for meta queries
}

// SQLQuery represents a formatted query object for the Grafana API.
type SQLQuery map[string]any

// BuildQuery constructs a query map suitable for Grafana's /api/ds/query endpoint.
func (*sqlEngine) BuildQuery(args BuildQueryArgs) SQLQuery {
	ds := map[string]any{
		"uid":  args.DatasourceUId,
		"type": (*args.DB).Type(),
	}

	return map[string]any{
		"refId":      args.RefID,
		"datasource": ds,
		"rawSql":     args.Query,
		"format":     "table",
		"intervalMs": args.IntervalMs,
	}
}

type QueryBatchArgs struct {
	From time.Time
	To   time.Time
}

// DSQueryResponse represents the raw API response from Grafana's /api/ds/query.
type DSQueryResponse struct {
	Results map[string]struct {
		Status int `json:"status,omitempty"`
		Frames []struct {
			Schema struct {
				Name   string `json:"name,omitempty"`
				RefID  string `json:"refId,omitempty"`
				Fields []struct {
					Name     string `json:"name"`
					Type     string `json:"type"`
					TypeInfo struct {
						Frame string `json:"frame,omitempty"`
					} `json:"typeInfo,omitempty"`
				} `json:"fields"`
			} `json:"schema"`
			Data struct {
				Values [][]interface{} `json:"values"`
			} `json:"data"`
		} `json:"frames,omitempty"`
		Error string `json:"error,omitempty"`
	} `json:"results"`
}

// QueryResFrame represents each row as a set of column-value pairs
//
// This format is more verbose than Grafana's native wide timeframe format but is
// easier for LLMs to consume as input.
type QueryResFrame struct {
	Name     string           `json:"name"`
	Columns  []string         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	RowCount uint             `json:"rowCount"`
}

type SQLQueryResult struct {
	Status int              `json:"status,omitempty"`
	Error  string           `json:"error,omitempty"`
	Frames []*QueryResFrame `json:"frames"`
}

type SQLQueryBatchResult struct {
	Results    map[string]*SQLQueryResult `json:"results"`
	ErrorCount uint
}

// QueryBatch executes multiple SQL queries against the Grafana API in a single request.
func (en *sqlEngine) QueryBatch(ctx context.Context, queries []SQLQuery, args QueryBatchArgs) (*SQLQueryBatchResult, error) {
	payload := map[string]interface{}{
		"queries": queries,
		"from":    strconv.FormatInt(args.From.UnixMilli(), 10),
		"to":      strconv.FormatInt(args.To.UnixMilli(), 10),
	}

	var response DSQueryResponse
	err := en.grafanaClient.Post(ctx, "/api/ds/query", payload, &response)
	if err != nil {
		return nil, err
	}

	result := SQLQueryBatchResult{
		Results: make(map[string]*SQLQueryResult),
	}

	for refID, r := range response.Results {

		frames := make([]*QueryResFrame, 0, len(r.Frames))

		for _, frame := range r.Frames {

			noOfCol := len(frame.Schema.Fields)
			if noOfCol == 0 {
				// columns not found for frame, skip frame
				continue
			}

			if len(frame.Data.Values) == 0 {
				// len(frame.Data.Values) equals len(frame.Schema.Fields)
				// this case shouldn't occur
				continue
			}

			// Number of rows count derived from count of values of first column
			noOfRows := (len(frame.Data.Values[0]))

			resFrame := QueryResFrame{}
			resFrame.Name = frame.Schema.Name
			resFrame.Columns = make([]string, 0, noOfCol)
			resFrame.Rows = make([]map[string]any, 0, noOfRows)
			resFrame.RowCount = uint(noOfRows)

			for colNo, field := range frame.Schema.Fields {

				fieldName := field.Name

				resFrame.Columns = append(resFrame.Columns, fieldName)

				for rowId, colValue := range frame.Data.Values[colNo] {
					if len(resFrame.Rows) < (rowId + 1) {
						resFrame.Rows = append(resFrame.Rows, make(map[string]any))
					}

					resFrame.Rows[rowId][fieldName] = colValue
				}
			}

			frames = append(frames, &resFrame)
		}
		frames = slices.Clip(frames)

		result.Results[refID] = &SQLQueryResult{
			Status: r.Status,
			Error:  r.Error,
			Frames: frames,
		}
		if r.Error != "" {
			result.ErrorCount++
		}

	}

	return &result, nil
}

func NewSQLEngine(ctx context.Context) (*sqlEngine, error) {
	grafanaClient, err := client.NewGrafanaClient(ctx)
	if err != nil {
		return nil, err
	}
	return &sqlEngine{
		grafanaClient: grafanaClient,
	}, nil
}
