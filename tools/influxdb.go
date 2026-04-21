package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/pkg/grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// InfluxDB constants define limits and data source types for the InfluxDB client.
const (
	InfluxDBDataSourceType = "influxdb"

	InfluxDBMaxLimit     uint = 1000
	InfluxDBDefaultLimit uint = 100

	InfluxDBMeasurementsDefaultLimit uint = 100
	// InfluxDBMeasurementsMaxLimit is the maximum limit applied when listing measurements.
	InfluxDBMeasurementsMaxLimit uint = 1000

	// InfluxDBTagsDefaultLimit is the default limit applied to fields and tags.
	InfluxDBTagsDefaultLimit uint = 100
	InfluxDBTagsMaxLimit     uint = 1000
)

const (
	// influxDBResponseLimitBytes is the max limit for successful query responses (10MB)
	influxDBResponseLimitBytes = 1024 * 1024 * 10
	// influxDBErrorResponseLimitBytes is the max limit for error responses (1MB)
	influxDBErrorResponseLimitBytes = 1024 * 1024
)

// Supported query types for the InfluxDB client.
const (
	FluxQueryType     = "Flux"
	SQLQueryType      = "SQL"
	InfluxQLQueryType = "InfluxQL"
)

// Regex expressions used for query parsing and limit enforcement.
var (
	// influxQLLimitRegEx matches InfluxQL LIMIT and optional OFFSET clauses.
	influxQLLimitRegEx = regexp.MustCompile(`(?i)(limit\s+)\d+(\s+offset\s+\d+)?(\s*$)`)

	// sqlCTEStartRegEx matches the start of a CTE (WITH clause).
	sqlCTEStartRegEx = regexp.MustCompile(`(?i)^\s*WITH\b`)

	// sqlKeywordRegEx matches standard SQL keywords that follow a CTE.
	sqlKeywordRegEx = regexp.MustCompile(`(?i)^(SELECT|INSERT|UPDATE|DELETE|MERGE|TRUNCATE)\b`)

	// fluxLimitRegEx matches a Flux limit operator at the end of a query.
	fluxLimitRegEx = regexp.MustCompile(`(?i)\|>\s*limit\s*\(\s*n\s*:\s*\d+\s*\)\s*$`)
)

type influxDBClient struct {
	httpClient *http.Client
	baseURL    string
}

// newInfluxDBClient creates a new InfluxDB client for the given datasource
// queryType: when non-nil used to restrict the datasource to have same queryType
// returns client along with query type of datasource
func newInfluxDBClient(ctx context.Context, uid string, queryType *string) (*influxDBClient, string, error) {
	// Verify the datasource exists and is a InfluxDB datasource
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, "", err
	}

	if ds.Type != InfluxDBDataSourceType {
		return nil, "", fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, InfluxDBDataSourceType)
	}

	// Verify the query lang specified is the one configured with datasource
	dsQueryType := InfluxQLQueryType

	if jsonMap, ok := ds.JSONData.(map[string]interface{}); ok {
		if dsQT, ok := jsonMap["version"].(string); ok && dsQT != "" {
			dsQueryType = dsQT
		}
	}

	if queryType != nil {
		if *queryType != dsQueryType {
			return nil, dsQueryType, fmt.Errorf("datasource %s is configured with querytype %s, not %s", uid, dsQueryType, *queryType)
		}

	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	// Create custom transport with TLS configuration if available
	var transport = http.DefaultTransport
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		var err error
		transport, err = tlsConfig.HTTPTransport(transport.(*http.Transport))
		if err != nil {
			return nil, dsQueryType, fmt.Errorf("failed to create custom transport: %w", err)
		}
	}

	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	return &influxDBClient{
		httpClient: client,
		baseURL:    baseURL,
	}, dsQueryType, nil
}

type InfluxQueryArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource to query. Use list_datasources to find available UIDs."`
	Query         string `json:"query" jsonschema:"required,description=SQL/Flux/InfluxQL query. Supports SQL macros: $__timeFilter for time filtering\\, $__timeFrom/$__timeTo for millisecond timestamps\\, $__interval for calculated intervals\\, $__dateBin(<column>)/$__dateBinAlias(<column>) to apply date_bin for timestamp columns. Supports Flux macros : v.timeRangeStart\\, v.timeRangeStop\\, v.windowPeriod (Grafana-calculated interval)\\, v.defaultBucket (configured default bucket)\\, v.organization (configured organization)\\."`
	QueryType     string `json:"queryType" jsonschema:"required,enum=SQL,enum=Flux,enum=InfluxQL,description=QueryType of Datasource. One of the specified options"`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End           string `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	IntervalMs    uint   `json:"intervalMs,omitempty" jsonschema:"description=Interval in milliseconds"`
	Limit         uint   `json:"limit,omitempty" jsonschema:"description=Limit number of records per table (or group)"`
}

// InfluxQueryResFrame represents a single frame of data in the query response.
type InfluxQueryResFrame struct {
	Name     string           `json:"name"`
	Columns  []string         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	RowCount uint             `json:"rowCount"`
}

// InfluxQueryResult contains the parsed results of an InfluxDB query.
type InfluxQueryResult struct {
	Frames      []*InfluxQueryResFrame `json:"frames"`
	FramesCount int                    `json:"framesCount"`
	Hints       *EmptyResultHints      `json:"hints,omitempty"`
}

type influxDBQueryPayload struct {
	Datasource struct {
		UID  string `json:"uid"`
		Type string `json:"type"`
	} `json:"datasource"`
	RefID        string `json:"refId"`
	Type         string `json:"type"`
	Format       string `json:"format"`
	IntervalMs   uint   `json:"intervalMs"`
	Query        string `json:"query"`
	RawSQL       string `json:"rawSql"`
	RawQuery     bool   `json:"rawQuery"`
	Limit        string `json:"limit"`
	ResultFormat string `json:"resultFormat"`
}

func (ic *influxDBClient) Query(ctx context.Context, args InfluxQueryArgs, from, to time.Time) (*grafana.DSQueryResponse, error) {
	format := "time_series"

	if args.QueryType == SQLQueryType {
		format = "table"
	}

	query := influxDBQueryPayload{
		Datasource: struct {
			UID  string `json:"uid"`
			Type string `json:"type"`
		}{
			UID:  args.DatasourceUID,
			Type: InfluxDBDataSourceType,
		},
		RefID:        "A",
		Type:         "timeSeriesQuery",
		Format:       format,
		IntervalMs:   args.IntervalMs,
		RawQuery:     true,
		Limit:        "",
		ResultFormat: "time_series",
	}

	// append query
	if args.QueryType == SQLQueryType {
		query.RawSQL = args.Query
	} else {
		query.Query = args.Query
	}

	payload := grafana.DSQueryPayload{
		Queries: []any{
			query,
		},
		From: strconv.FormatInt(from.UnixMilli(), 10),
		To:   strconv.FormatInt(to.UnixMilli(), 10),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	url := ic.baseURL + "/api/ds/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ic.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, influxDBErrorResponseLimitBytes))
		return nil, fmt.Errorf("InfluxDB query returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read and parse response
	var queryResp grafana.DSQueryResponse
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, influxDBResponseLimitBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if err := unmarshalJSONWithLimitMsg(bodyBytes, &queryResp, influxDBResponseLimitBytes); err != nil {
		return nil, err
	}

	return &queryResp, nil
}

func findTopLevelSelectAfterCTE(query string) int {
	loc := sqlCTEStartRegEx.FindStringIndex(query)
	if loc == nil {
		return -1
	}
	i := loc[1]

	for i < len(query) {
		parenIdx := strings.Index(query[i:], "(")
		if parenIdx == -1 {
			break
		}
		i += parenIdx

		depth := 0
		for i < len(query) {
			switch query[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			i++
			if depth == 0 {
				break
			}
		}

		// Skip whitespace
		for i < len(query) && (query[i] == ' ' || query[i] == '\n' || query[i] == '\t' || query[i] == '\r') {
			i++
		}

		if i >= len(query) {
			return -1 // nothing after closing paren — malformed
		}

		if query[i] == ',' {
			i++ // another CTE follows
		} else {
			// Verify a valid SQL keyword exists here (SELECT, INSERT, UPDATE, DELETE, etc.)
			if !sqlKeywordRegEx.MatchString(query[i:]) {
				return -1
			}
			return i
		}
	}
	return -1
}

func enforceQueryLimit(args *InfluxQueryArgs) {
	// flux, influxql limits per measurement(influxql), table(flux) level so number of measurements * limit is final records
	// sql limit applies on final records level

	limit := InfluxDBDefaultLimit

	if args.Limit >= InfluxDBMaxLimit {
		limit = InfluxDBMaxLimit
	} else if args.Limit > 0 {
		limit = args.Limit
	}
	switch args.QueryType {

	case SQLQueryType:
		query := strings.TrimSuffix(args.Query, ";")

		if sqlCTEStartRegEx.MatchString(query) {
			// CTE query
			pos := findTopLevelSelectAfterCTE(query)
			if pos != -1 {
				ctePrefix := query[:pos]  // WITH a AS (...), b AS (...)
				selectPart := query[pos:] // SELECT * FROM a JOIN b ON true

				// wrap select with limit
				wrappedSelect := "(" + selectPart + ")" + fmt.Sprintf(" LIMIT %d", limit)
				args.Query = ctePrefix + wrappedSelect
			}
		} else {
			// window functions , generic queries
			// wrap query and apply limit
			args.Query = "(" + query + ")" + fmt.Sprintf(" LIMIT %d", limit)
		}
	case InfluxQLQueryType:
		// override limits when query contains limit
		if influxQLLimitRegEx.Match([]byte(args.Query)) {
			replacement := fmt.Sprintf("${1}%d${2}${3}", limit)
			args.Query = influxQLLimitRegEx.ReplaceAllString(args.Query, replacement)
		} else {
			// append limit in other cases
			query := strings.TrimSuffix(args.Query, ";")
			args.Query = query + fmt.Sprintf(" LIMIT %d", limit)
		}
	case FluxQueryType:
		query := strings.TrimSpace(args.Query)

		if fluxLimitRegEx.MatchString(query) {
			// Replace existing limit at end
			args.Query = fluxLimitRegEx.ReplaceAllString(query, fmt.Sprintf("|> limit(n:%d)", limit))
		} else {
			// Always append limit at end — goal is to always have limit as final operator
			args.Query = query + fmt.Sprintf("\n|> limit(n:%d)", limit)
		}

	}

}

func parseTimeRange(start string, end string) (*time.Time, *time.Time, error) {
	// Parse time range
	defaultPeriod := time.Hour

	now := time.Now()
	fromTime := now.Add(-1 * defaultPeriod) // Default: 1 hour ago
	toTime := now                           // Default: now

	if start != "" {
		parsed, err := parseStartTime(start)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}

		// set relative end time 1hour from start
		if end == "" {
			toTime = fromTime.Add(defaultPeriod)
		}
	}

	if end != "" {
		parsed, err := parseEndTime(end)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}

		if start == "" {
			fromTime = toTime.Add(-1 * defaultPeriod)
		}
	}

	return &fromTime, &toTime, nil

}

// parseQueryResponseFrames parses ds/query response in a json key-pair format
// returns list of frames combined of query results
// treats empty results as an error
func parseQueryResponseFrames(resp *grafana.DSQueryResponse) ([]*InfluxQueryResFrame, error) {
	frames := make([]*InfluxQueryResFrame, 0)
	hasResults := false

	// InfluxQL Query has a frame for each column selection, (different selection sets result in varying row count for each frame)
	// SQL Query results in a single frame , selected columns are mapped in frame.columns
	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		// grow slice to accomadte atleast len(r.Frames) elements
		frames = slices.Grow(frames, len(r.Frames))

		for _, frame := range r.Frames {

			noOfCol := len(frame.Schema.Fields)
			if noOfCol == 0 {
				// columns not found for frame, skip frame
				continue
			}

			resFrame := InfluxQueryResFrame{}
			resFrame.Columns = make([]string, 0, noOfCol)

			if len(frame.Data.Values) == 0 {
				continue
			}

			if len(frame.Data.Values) != noOfCol {
				// return error when data values count mismatch schema fields
				return nil, fmt.Errorf("frame data values count (%d) mismatch schema fields count (%d)", len(frame.Data.Values), noOfCol)
			}

			// Number of rows count derived from count of values of first column
			rowCount := (len(frame.Data.Values[0]))
			resFrame.RowCount = uint(rowCount)
			resFrame.Rows = make([]map[string]any, 0, rowCount)
			resFrame.Name = frame.Schema.Name

			for colNo, field := range frame.Schema.Fields {

				fieldName := field.Name

				if field.Labels["_field"] != "" && field.Name == "_value" {
					// use field name for column values of flux queries
					fieldName = field.Labels["_field"]
				}
				// influxql query with 'time_series' format query
				if field.Config != nil {
					if displayName, ok := field.Config["displayNameFromDS"].(string); ok && displayName != "" {
						fieldName = displayName
					}
				}

				resFrame.Columns = append(resFrame.Columns, fieldName)

				for rowId, colValue := range frame.Data.Values[colNo] {
					if len(resFrame.Rows) < (rowId + 1) {
						resFrame.Rows = append(resFrame.Rows, make(map[string]any))
					}

					resFrame.Rows[rowId][fieldName] = colValue
				}
			}

			frames = append(frames, &resFrame)
			if rowCount > 0 && !hasResults {
				hasResults = true
			}
		}
	}

	var err error
	if !hasResults {
		err = grafana.ErrNoRows
	}
	frames = slices.Clip(frames)

	return frames, err
}
func queryInflux(ctx context.Context, args InfluxQueryArgs) (*InfluxQueryResult, error) {
	client, _, err := newInfluxDBClient(ctx, args.DatasourceUID, &args.QueryType)

	if err != nil {
		return nil, err
	}

	originalQuery := args.Query

	enforceQueryLimit(&args)
	from, to, err := parseTimeRange(args.Start, args.End)
	if err != nil {
		return nil, err
	}

	resp, err := client.Query(ctx, args, *from, *to)
	if err != nil {
		return nil, err
	}

	result := InfluxQueryResult{}

	frames, err := parseQueryResponseFrames(resp)

	if err != nil {
		if !errors.Is(err, grafana.ErrNoRows) {
			return nil, err
		}
		// query response returned no rows
		// respond sucess with hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: InfluxDBDataSourceType,
			Query:          originalQuery,
			ProcessedQuery: args.Query,
			StartTime:      *from,
			EndTime:        *to,
		})
	}

	result.Frames = frames
	result.FramesCount = len(result.Frames)

	return &result, nil
}

var QueryInfluxDB = mcpgrafana.MustTool(
	"query_influxdb",
	"Queries InfluxDB datasource, supports one of Flux, SQL, or InfluxQL query languages. Use in order: list_datasources -> get_datasource to determine query language configured for datasource.Use both list_influxdb_field_keys , list_influxdb_tag_keys to determine the available columns",
	queryInflux,
	mcp.WithTitleAnnotation("Query InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListBucketArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
}
type ListBucketResult struct {
	Buckets     []string          `json:"buckets"`
	BucketCount uint              `json:"bucketCount"`
	Hints       *EmptyResultHints `json:"hints,omitempty"`
}

// extractColValues extracts Values from response of string type columns
func extractColValues(resp *grafana.DSQueryResponse, colName string) ([]string, error) {
	fieldValues := make([]string, 0)

	for _, result := range resp.Results {

		if result.Error != "" {
			return nil, errors.New(result.Error)
		}

		for _, frame := range result.Frames {
			fieldColIdx := -1

			for idx, field := range frame.Schema.Fields {
				if field.Name == colName {
					fieldColIdx = idx
					break
				}
			}

			if fieldColIdx == -1 {
				// no bucket name col found
				continue
			}

			if len(frame.Data.Values) <= fieldColIdx {
				continue
			}

			fieldValues = slices.Grow(fieldValues, len(frame.Data.Values[fieldColIdx]))

			for _, name := range frame.Data.Values[fieldColIdx] {
				if s, ok := name.(string); ok {
					fieldValues = append(fieldValues, s)
				} else {
					return nil, fmt.Errorf("expected column %s to be string type, got %T", colName, name)
				}
			}
		}
	}

	return fieldValues, nil
}

func listBuckets(ctx context.Context, args ListBucketArgs) (*ListBucketResult, error) {
	queryType := FluxQueryType
	client, sourceQueryType, err := newInfluxDBClient(ctx, args.DatasourceUID, &queryType)

	if err != nil {
		if sourceQueryType != "" && sourceQueryType != queryType {
			return nil, fmt.Errorf("datasource is not configured with Flux, bucket listing is explicit to Flux linked datasources")
		}
		return nil, err
	}

	query := "buckets()"

	refTime := time.Now()

	response, err := client.Query(ctx, InfluxQueryArgs{DatasourceUID: args.DatasourceUID, Query: query, QueryType: FluxQueryType, Start: "", End: ""}, refTime, refTime)

	if err != nil {
		return nil, err
	}

	buckets, err := extractColValues(response, "name")

	if err != nil {
		return nil, err
	}

	result := ListBucketResult{}

	if len(buckets) == 0 {
		// return empty result hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: InfluxDBDataSourceType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("empty results, check if buckets exist for connected datasources"),
		})
	}

	result.BucketCount = uint(len(buckets))
	result.Buckets = buckets
	return &result, nil
}

var ListBucketsInflux = mcpgrafana.MustTool(
	"list_influxdb_buckets",
	"Lists buckets of an InfluxDB datasource identified by its UID. Requires the datasource to be configured with Flux. Use in order: list_datasources -> get_datasource -> list_influxdb_buckets",
	listBuckets,
	mcp.WithTitleAnnotation("List Buckets InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListMeasurementsArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
	Bucket        string `json:"bucket,omitempty" jsonschema:"optional,description=Bucket Name of target bucket to fetch from; required only for Flux linked datasources."`
	Limit         uint   `json:"limit,omitempty"`
}

type ListMeasurementResult struct {
	Measurements     []string          `json:"measurements"`
	MeasurementCount uint              `json:"measurementCount"`
	Hints            *EmptyResultHints `json:"hints,omitempty"`
}

func enforceMeasurementsLimit(args *ListMeasurementsArgs) {
	if args.Limit > InfluxDBMeasurementsMaxLimit {
		args.Limit = InfluxDBMeasurementsMaxLimit
	}
	if args.Limit == 0 {
		args.Limit = InfluxDBMeasurementsDefaultLimit
	}
}
func listMeasurements(ctx context.Context, args ListMeasurementsArgs) (*ListMeasurementResult, error) {
	client, queryType, err := newInfluxDBClient(ctx, args.DatasourceUID, nil)
	if err != nil {
		return nil, err
	}

	enforceMeasurementsLimit(&args)

	if queryType == FluxQueryType && args.Bucket == "" {
		return nil, fmt.Errorf("bucket is required for %s linked InfluxDB datasources", FluxQueryType)
	}
	var query string
	// represents column key of measurement in response
	var colKey string
	switch queryType {
	case SQLQueryType:
		query = fmt.Sprintf("SELECT table_name FROM information_schema.tables WHERE table_schema = 'iox' ORDER BY table_name LIMIT %d", args.Limit)
		colKey = "table_name"
	case FluxQueryType:
		query = fmt.Sprintf(
			`import "influxdata/influxdb/schema"
		 	schema.measurements(bucket: %s)|> limit(n: %d)`,
			quoteStringAsFluxLiteral(args.Bucket), args.Limit)
		colKey = "_value"
	case InfluxQLQueryType:
		query = fmt.Sprintf("SHOW MEASUREMENTS LIMIT %d", args.Limit)
		colKey = "Value"
	}

	refTime := time.Now()
	response, err := client.Query(ctx, InfluxQueryArgs{DatasourceUID: args.DatasourceUID, Query: query, QueryType: queryType, Start: "", End: ""}, refTime, refTime)

	if err != nil {
		return nil, err
	}

	measurements, err := extractColValues(response, colKey)

	if err != nil {
		return nil, err
	}

	result := ListMeasurementResult{}

	if len(measurements) == 0 {
		// add empty results hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: InfluxDBDataSourceType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("no measurements found, verify at datasource"),
		})
	}

	result.MeasurementCount = uint(len(measurements))
	result.Measurements = measurements
	return &result, nil
}

var ListMeasurements = mcpgrafana.MustTool(
	"list_influxdb_measurements",
	"Lists Measurements of an InfluxDB datasource identified by its UID. Use in order: list_datasources -> get_datasource -> list_influxdb_buckets (required only for Flux linked datasource) -> list_influxdb_measurements",
	listMeasurements,
	mcp.WithTitleAnnotation("List Measurements InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListTagKeysArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
	Bucket        string `json:"bucket,omitempty" jsonschema:"optional,description=Bucket Name of target bucket to fetch from\\,required only for Flux linked datasources."`
	Measurement   string `json:"measurement" jsonschema:"required,description=Filter by measurement"`
	Limit         uint   `json:"limit,omitempty"`
}
type ListTagKeysResult struct {
	TagKeys      []string          `json:"tags"`
	TagKeysCount uint              `json:"tagCount"`
	Hints        *EmptyResultHints `json:"hints,omitempty"`
}

func enforceTagKeysLimit(args *ListTagKeysArgs) {
	if args.Limit > InfluxDBTagsMaxLimit {
		args.Limit = InfluxDBTagsMaxLimit
	}
	if args.Limit == 0 {
		args.Limit = InfluxDBTagsDefaultLimit
	}
}

func quoteStringAsLiteral(s string) string {
	// SQL style: single quotes, escape internal single quotes by doubling
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func quoteStringAsFluxLiteral(s string) string {
	// Flux style: double quotes, escape backslash then double quotes
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// quoteStringAsInfluxQLIdentifier quotes a string as an InfluxQL identifier using double quotes.
func quoteStringAsInfluxQLIdentifier(s string) string {
	// Must escape backslashes FIRST, then double quotes
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func listTagKeys(ctx context.Context, args ListTagKeysArgs) (*ListTagKeysResult, error) {
	enforceTagKeysLimit(&args)

	client, queryType, err := newInfluxDBClient(ctx, args.DatasourceUID, nil)

	if err != nil {
		return nil, err
	}

	if queryType == FluxQueryType && args.Bucket == "" {
		return nil, fmt.Errorf("bucket is required for %s linked InfluxDB datasources", FluxQueryType)
	}

	var tagColumnKey string
	var query string

	switch queryType {
	case SQLQueryType:
		// data_type 'Dictionary%%' distinguishes tags from fields for SQL QUERIES
		query = fmt.Sprintf("SELECT column_name FROM information_schema.columns WHERE table_schema = 'iox' AND table_name = %s AND data_type LIKE 'Dictionary%%' ORDER BY column_name LIMIT %d",
			quoteStringAsLiteral(args.Measurement), args.Limit)
		tagColumnKey = "column_name"
	case FluxQueryType:
		query = fmt.Sprintf(
			`import "influxdata/influxdb/schema"
		 	schema.measurementTagKeys(bucket: %s, measurement: %s)|> limit(n: %d)`,
			quoteStringAsFluxLiteral(args.Bucket), quoteStringAsFluxLiteral(args.Measurement), args.Limit)
		tagColumnKey = "_value"
	case InfluxQLQueryType:
		query = fmt.Sprintf(`SHOW TAG KEYS FROM %s LIMIT %d`,
			quoteStringAsInfluxQLIdentifier(args.Measurement), args.Limit)
		tagColumnKey = "Value"
	}

	refTime := time.Now()
	response, err := client.Query(ctx, InfluxQueryArgs{DatasourceUID: args.DatasourceUID, Query: query, QueryType: queryType, Start: "", End: ""}, refTime, refTime)

	if err != nil {
		return nil, err
	}

	tags, err := extractColValues(response, tagColumnKey)

	if err != nil {
		return nil, err
	}

	result := ListTagKeysResult{}

	if len(tags) == 0 {
		// add empty results hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: InfluxDBDataSourceType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("no tags found, verify at datasource"),
		})
	}

	result.TagKeysCount = uint(len(tags))
	result.TagKeys = tags
	return &result, nil
}

var ListTagKeys = mcpgrafana.MustTool(
	"list_influxdb_tag_keys",
	"Lists Tag Keys of an InfluxDB datasource identified by its UID. Use in order: list_datasources -> get_datasource -> list_influxdb_buckets (required only for Flux linked datasource) -> list_influxdb_measurements -> list_influxdb_tag_keys",
	listTagKeys,
	mcp.WithTitleAnnotation("List Tag Keys InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListFieldKeysArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
	Bucket        string `json:"bucket,omitempty" jsonschema:"optional,description=Bucket Name of target bucket to fetch from\\,required only for Flux linked datasources."`
	Measurement   string `json:"measurement" jsonschema:"required,description=Filter by measurement"`
	Limit         uint   `json:"limit,omitempty"`
}

type ListFieldKeysResult struct {
	FieldKeys      []string          `json:"fields"`
	FieldKeysCount uint              `json:"fieldCount"`
	Hints          *EmptyResultHints `json:"hints,omitempty"`
}

// enforceFieldKeysLimit applies the default or maximum limits to the provided field keys arguments.
func enforceFieldKeysLimit(args *ListFieldKeysArgs) {
	if args.Limit > InfluxDBTagsMaxLimit {
		args.Limit = InfluxDBTagsMaxLimit
	}
	if args.Limit == 0 {
		args.Limit = InfluxDBTagsDefaultLimit
	}
}

func listFieldKeys(ctx context.Context, args ListFieldKeysArgs) (*ListFieldKeysResult, error) {
	enforceFieldKeysLimit(&args)

	client, queryType, err := newInfluxDBClient(ctx, args.DatasourceUID, nil)

	if err != nil {
		return nil, err
	}

	if queryType == FluxQueryType && args.Bucket == "" {
		return nil, fmt.Errorf("bucket is required for %s linked InfluxDB datasources", FluxQueryType)
	}

	var fieldColumnKey string
	var query string

	switch queryType {
	case SQLQueryType:
		// data_type 'Dictionary%%' distinguishes tags from fields for SQL QUERIES
		query = fmt.Sprintf("SELECT column_name FROM information_schema.columns WHERE table_schema = 'iox' AND table_name = %s AND data_type NOT LIKE 'Dictionary%%' ORDER BY column_name LIMIT %d",
			quoteStringAsLiteral(args.Measurement), args.Limit)
		fieldColumnKey = "column_name"
	case FluxQueryType:
		query = fmt.Sprintf(
			`import "influxdata/influxdb/schema"
		     schema.measurementFieldKeys(bucket: %s, measurement: %s)|> limit(n: %d)`,
			quoteStringAsFluxLiteral(args.Bucket), quoteStringAsFluxLiteral(args.Measurement), args.Limit)
		fieldColumnKey = "_value"
	case InfluxQLQueryType:
		query = fmt.Sprintf(`SHOW FIELD KEYS FROM %s LIMIT %d`,
			quoteStringAsInfluxQLIdentifier(args.Measurement), args.Limit)
		fieldColumnKey = "Value"
	}

	refTime := time.Now()
	response, err := client.Query(ctx, InfluxQueryArgs{DatasourceUID: args.DatasourceUID, Query: query, QueryType: queryType, Start: "", End: ""}, refTime, refTime)

	if err != nil {
		return nil, err
	}

	fieldKeys, err := extractColValues(response, fieldColumnKey)

	if err != nil {
		return nil, err
	}

	result := ListFieldKeysResult{}

	if len(fieldKeys) == 0 {
		// add empty results hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: InfluxDBDataSourceType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("no fields found, verify at datasource"),
		})
	}

	result.FieldKeysCount = uint(len(fieldKeys))
	result.FieldKeys = fieldKeys
	return &result, nil
}

var ListFieldKeys = mcpgrafana.MustTool(
	"list_influxdb_field_keys",
	"Lists Field Keys of an InfluxDB datasource identified by its UID. Use in order: list_datasources -> get_datasource -> list_influxdb_buckets (required only for Flux linked datasource) -> list_influxdb_measurements -> list_influxdb_field_keys",
	listFieldKeys,
	mcp.WithTitleAnnotation("List Field Keys InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddInfluxTools(server *server.MCPServer) {
	QueryInfluxDB.Register(server)
	ListBucketsInflux.Register(server)
	ListMeasurements.Register(server)
	ListTagKeys.Register(server)
	ListFieldKeys.Register(server)
}
