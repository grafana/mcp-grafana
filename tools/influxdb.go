package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// InfluxDBDatasourceType is the type identifier for built-in InfluxDB datasources.
	InfluxDBDatasourceType = "influxdb"

	// DefaultInfluxDBMaxDataPoints is the default maxDataPoints value forwarded
	// to /api/ds/query when the caller doesn't specify one. Matches the number
	// of points Grafana's own UI requests for a typical panel.
	DefaultInfluxDBMaxDataPoints = 1000

	// InfluxDB dialects (forwarded to Grafana as the "queryType" field).
	InfluxDBDialectInfluxQL = "influxql"
	InfluxDBDialectFlux     = "flux"
)

// InfluxDBQueryParams defines the parameters for querying an InfluxDB datasource.
type InfluxDBQueryParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource to query. Use list_datasources to find available UIDs."`
	Query         string `json:"query" jsonschema:"required,description=Raw query string. InfluxQL for v1.x datasources (e.g. SELECT * FROM cpu WHERE time > now() - 1h)\\, or Flux for v2.x datasources (e.g. from(bucket: \"mybucket\") |> range(start: -1h))."`
	Dialect       string `json:"dialect,omitempty" jsonschema:"description=Query dialect: 'influxql' or 'flux'. If omitted\\, inferred from the datasource's configured query language (v1 -> influxql\\, v2 -> flux)."`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time for the query. Time formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to 1 hour ago."`
	End           string `json:"end,omitempty" jsonschema:"description=End time for the query. Time formats: 'now'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to now."`
	MaxDataPoints int    `json:"maxDataPoints,omitempty" jsonschema:"description=Maximum number of data points to return. Default: 1000."`
}

// InfluxDBQueryResult is the normalized result returned to the MCP client.
//
// Grafana's /api/ds/query response is passed through as-is (in RawFrames) so
// callers can inspect the native frame metadata when they need it, while
// Columns/Rows/RowCount give an easy-to-consume tabular view for simple cases.
type InfluxDBQueryResult struct {
	Columns   []string                 `json:"columns"`
	Rows      []map[string]interface{} `json:"rows"`
	RowCount  int                      `json:"rowCount"`
	Dialect   string                   `json:"dialect"`
	RawFrames json.RawMessage          `json:"rawFrames,omitempty"`
	Hints     *EmptyResultHints        `json:"hints,omitempty"`
}

// newInfluxDBDatasource verifies the datasource exists and is an InfluxDB
// datasource, returning the datasource metadata for dialect inference.
func newInfluxDBDatasource(ctx context.Context, uid string) (*models.DataSource, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	if ds.Type != InfluxDBDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, InfluxDBDatasourceType)
	}

	return ds, nil
}

// resolveInfluxDBDialect returns a canonical dialect string.
//
// If the user supplied one, it's validated. Otherwise we try to infer it from
// the datasource's jsonData.version field, which Grafana sets to "InfluxQL",
// "Flux", or "SQL" depending on how the datasource was configured. We fall back
// to InfluxQL since it's the v1 default and the most common deployment.
func resolveInfluxDBDialect(requested string, jsonData map[string]interface{}) (string, error) {
	if requested != "" {
		switch strings.ToLower(requested) {
		case InfluxDBDialectInfluxQL:
			return InfluxDBDialectInfluxQL, nil
		case InfluxDBDialectFlux:
			return InfluxDBDialectFlux, nil
		default:
			return "", fmt.Errorf("unsupported dialect %q: must be one of influxql, flux", requested)
		}
	}

	if v, ok := jsonData["version"].(string); ok {
		switch strings.ToLower(v) {
		case "influxql":
			return InfluxDBDialectInfluxQL, nil
		case "flux":
			return InfluxDBDialectFlux, nil
		}
	}
	return InfluxDBDialectInfluxQL, nil
}

// buildInfluxDBPayload constructs the /api/ds/query request body. Kept as a
// separate function so unit tests can verify the exact JSON we send upstream.
func buildInfluxDBPayload(datasourceUID, dialect, query string, from, to time.Time, maxDataPoints int) map[string]interface{} {
	if maxDataPoints <= 0 {
		maxDataPoints = DefaultInfluxDBMaxDataPoints
	}

	q := map[string]interface{}{
		"refId": "A",
		"datasource": map[string]string{
			"uid":  datasourceUID,
			"type": InfluxDBDatasourceType,
		},
		"query":         query,
		"rawQuery":      true,
		"queryType":     dialect,
		"maxDataPoints": maxDataPoints,
	}

	return dsQueryPayload(from, to, q)
}

func queryInfluxDB(ctx context.Context, args InfluxDBQueryParams) (*InfluxDBQueryResult, error) {
	if strings.TrimSpace(args.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	ds, err := newInfluxDBDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating InfluxDB client: %w", err)
	}

	// grafana-openapi types JSONData as interface{}; the InfluxDB plugin
	// stores version/org/bucket there as a map. Same pattern as
	// alerting_contact_points.go and prom_backend.go.
	jsonData, _ := ds.JSONData.(map[string]interface{})
	dialect, err := resolveInfluxDBDialect(args.Dialect, jsonData)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	fromTime := now.Add(-1 * time.Hour)
	toTime := now

	if args.Start != "" {
		parsed, err := parseStartTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}
	}
	if args.End != "" {
		parsed, err := parseEndTime(args.End)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}
	}

	payload := buildInfluxDBPayload(args.DatasourceUID, dialect, args.Query, fromTime, toTime, args.MaxDataPoints)

	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := doDSQuery(ctx, client, baseURL, payload)
	if err != nil {
		return nil, err
	}

	columns, rows, err := framesToTabularRows(resp)
	if err != nil {
		return nil, err
	}

	result := &InfluxDBQueryResult{
		Columns:  columns,
		Rows:     rows,
		RowCount: len(rows),
		Dialect:  dialect,
	}

	// Preserve the raw frames so callers that want the native
	// Grafana shape (timestamps + labels per field) can still get it.
	for _, r := range resp.Responses {
		if len(r.Frames) > 0 {
			rawFramesJSON, err := json.Marshal(r.Frames)
			if err == nil {
				result.RawFrames = rawFramesJSON
			}
			break
		}
	}

	if result.RowCount == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "influxdb",
			Query:          args.Query,
			StartTime:      fromTime,
			EndTime:        toTime,
		})
	}
	return result, nil
}

var QueryInfluxDB = mcpgrafana.MustTool(
	"query_influxdb",
	`Query an InfluxDB datasource via Grafana. Supports both InfluxQL (v1.x) and Flux (v2.x). The 'dialect' parameter selects the query language; if omitted it's inferred from the datasource configuration.

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)

InfluxQL example: SELECT mean("value") FROM "cpu" WHERE time > now() - 1h GROUP BY time(1m)
Flux example:    from(bucket: "metrics") |> range(start: -1h) |> filter(fn: (r) => r._measurement == "cpu")`,
	queryInfluxDB,
	mcp.WithTitleAnnotation("Query InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddInfluxDBTools registers all InfluxDB tools with the MCP server.
func AddInfluxDBTools(mcp *server.MCPServer) {
	QueryInfluxDB.Register(mcp)
}
