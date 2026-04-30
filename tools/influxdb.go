package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

// influxDBQueryResponse mirrors the ClickHouse response shape — Grafana's
// /api/ds/query returns the same envelope for any datasource.
type influxDBQueryResponse struct {
	Results map[string]struct {
		Status int               `json:"status,omitempty"`
		Frames []json.RawMessage `json:"frames,omitempty"`
		Error  string            `json:"error,omitempty"`
	} `json:"results"`
}

// influxDBFrame is a structural view of a single data frame, used to flatten
// columnar frame data into row-oriented results.
type influxDBFrame struct {
	Schema struct {
		Name   string `json:"name,omitempty"`
		RefID  string `json:"refId,omitempty"`
		Fields []struct {
			Name   string            `json:"name"`
			Type   string            `json:"type,omitempty"`
			Labels map[string]string `json:"labels,omitempty"`
		} `json:"fields"`
	} `json:"schema"`
	Data struct {
		Values [][]interface{} `json:"values"`
	} `json:"data"`
}

type influxDBClient struct {
	httpClient *http.Client
	baseURL    string
}

// newInfluxDBClient builds the HTTP client plumbing and returns it alongside
// the resolved datasource. Callers that need datasource metadata (dialect
// inference, jsonData) should reuse the returned *models.DataSource rather
// than issuing a second getDatasourceByUID call.
func newInfluxDBClient(ctx context.Context, uid string) (*influxDBClient, *models.DataSource, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, nil, err
	}

	if ds.Type != InfluxDBDatasourceType {
		return nil, nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, InfluxDBDatasourceType)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := cfg.URL

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create transport: %w", err)
	}

	return &influxDBClient{
		httpClient: &http.Client{Transport: transport},
		baseURL:    baseURL,
	}, ds, nil
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

	return map[string]interface{}{
		"queries": []map[string]interface{}{q},
		"from":    strconv.FormatInt(from.UnixMilli(), 10),
		"to":      strconv.FormatInt(to.UnixMilli(), 10),
	}
}

func (c *influxDBClient) query(ctx context.Context, payload map[string]interface{}) (*influxDBQueryResponse, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	url := c.baseURL + "/api/ds/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("InfluxDB query returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var bytesLimit int64 = 1024 * 1024 * 10 // 10MB
	body := io.LimitReader(resp.Body, bytesLimit)
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var queryResp influxDBQueryResponse
	if err := unmarshalJSONWithLimitMsg(bodyBytes, &queryResp, int(bytesLimit)); err != nil {
		return nil, err
	}
	return &queryResp, nil
}

// framesToRows flattens Grafana's columnar frame data into row-oriented maps,
// matching the ClickHouse tool's output shape.
func framesToRows(rawFrames []json.RawMessage) ([]string, []map[string]interface{}, error) {
	var columns []string
	var rows []map[string]interface{}

	for _, raw := range rawFrames {
		var frame influxDBFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			return nil, nil, fmt.Errorf("parsing frame: %w", err)
		}

		fieldNames := make([]string, len(frame.Schema.Fields))
		for i, f := range frame.Schema.Fields {
			fieldNames[i] = f.Name
		}
		// Columns from the last non-empty frame win. InfluxQL range queries
		// usually return a single frame; Flux queries may return several, one
		// per table, but they share the same field schema.
		if len(fieldNames) > 0 {
			columns = fieldNames
		}

		if len(frame.Data.Values) == 0 {
			continue
		}

		rowCount := len(frame.Data.Values[0])
		for i := 0; i < rowCount; i++ {
			row := make(map[string]interface{}, len(fieldNames))
			for colIdx, colName := range fieldNames {
				if colIdx < len(frame.Data.Values) && i < len(frame.Data.Values[colIdx]) {
					row[colName] = frame.Data.Values[colIdx][i]
				}
			}
			rows = append(rows, row)
		}
	}
	return columns, rows, nil
}

func queryInfluxDB(ctx context.Context, args InfluxDBQueryParams) (*InfluxDBQueryResult, error) {
	if strings.TrimSpace(args.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	client, ds, err := newInfluxDBClient(ctx, args.DatasourceUID)
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
	resp, err := client.query(ctx, payload)
	if err != nil {
		return nil, err
	}

	result := &InfluxDBQueryResult{
		Columns: []string{},
		Rows:    []map[string]interface{}{},
		Dialect: dialect,
	}

	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}
		if len(r.Frames) == 0 {
			continue
		}

		// Preserve the raw frames so callers that want the native
		// Grafana shape (timestamps + labels per field) can still get it.
		rawFramesJSON, err := json.Marshal(r.Frames)
		if err == nil {
			result.RawFrames = rawFramesJSON
		}

		cols, rows, err := framesToRows(r.Frames)
		if err != nil {
			return nil, err
		}
		if len(cols) > 0 {
			result.Columns = cols
		}
		result.Rows = append(result.Rows, rows...)
	}

	result.RowCount = len(result.Rows)
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
