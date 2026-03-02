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
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	InfluxDBDataSourceType = "influxdb"

	InfluxDBMaxLimit     uint = 1000
	InfluxDBDefaultLimit uint = 100

	InfluxDBMeasurementsDefaultLimit uint = 100
	InfluxDBMeasurementsMaxLimit     uint = 1000

	//limit applied to fields , tags
	InfluxDbTagsDefaultLimit uint = 100
	InfluxDbTagsMaxLimit     uint = 1000
)

const (
	FluxQueryType     = "Flux"
	SQLQueryType      = "SQL"
	InfluxQLQueryType = "InfluxQL"
)

type influxDBClient struct {
	httpClient *http.Client
	baseURL    string
}

// newInfluxDBClient creates a new InfluxDB client for the given datasource
// queryType: when non-nil used to restict the datasource to have same queryType
// returns client along with querytype of datasource
func newInfluxDBClient(ctx context.Context, uid string, queryType *string) (*influxDBClient, string, error) {
	// Verify the datasource exists and is a InfluxDB datasource
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, "", err
	}

	if ds.Type != InfluxDBDataSourceType {
		return nil, "", fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, InfluxDBDataSourceType)
	}

	//verify the query lang specified is the one confgured with datasource
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
	QueryType     string `json:"query_type" jsonschema:"required,enum=SQL,enum=Flux,enum=InfluxQL,description=QueryType of Datasource one of the specified options"`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now-1h"`
	End           string `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T20:00:00Z'\\, '1738522800000' (Unix ms). Default: now"`
	IntervalMs    uint
	Limit         uint `json:"limit"`
}

// influxQueryResponse represents the raw API response from Grafana's /api/ds/query
type influxQueryResponse struct {
	Results map[string]struct {
		Status int `json:"status,omitempty"`
		Frames []struct {
			Schema struct {
				Name   string `json:"name,omitempty"`
				RefID  string `json:"refId,omitempty"`
				Fields []struct {
					Labels struct {
						Field string `json:"_field,omitempty"`
					} `json:"labels"`
					Name     string `json:"name"`
					Type     string `json:"type"`
					TypeInfo struct {
						Frame string `json:"frame,omitempty"`
					} `json:"typeInfo,omitempty"`
				} `json:"fields"`
			} `json:"schema,omitempty"`
			Data struct {
				Values [][]interface{} `json:"values"`
			} `json:"data"`
		} `json:"frames,omitempty"`
		Error string `json:"error,omitempty"`
	} `json:"results"`
}

type InfluxQueryResFrame struct {
	Name     string
	Columns  []string
	Rows     []map[string]any
	RowCount uint
}
type InfluxQueryResult struct {
	Frames      []*InfluxQueryResFrame
	FramesCount int
	Hints       *EmptyResultHints `json:"hints,omitempty"`
}

func queryTypePayloadKey(queryType string) (string, error) {
	if queryType == SQLQueryType {
		return "rawSql", nil
	}

	if queryType == InfluxQLQueryType || queryType == FluxQueryType {
		return "query", nil
	}

	return "", fmt.Errorf("unknown query type: %s", queryType)
}

func (ic *influxDBClient) Query(ctx context.Context, args InfluxQueryArgs, from, to time.Time) (*influxQueryResponse, error) {
	queryPayloadKey, err := queryTypePayloadKey(args.QueryType)

	if err != nil {
		//pass errors
		return nil, err
	}
	format := "time_series"

	if args.QueryType == SQLQueryType {
		format = "table"
	}

	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"datasource": map[string]string{
					"uid":  args.DatasourceUID,
					"type": InfluxDBDataSourceType,
				},
				"refId":         "A",
				"type":          "timeSeriesQuery",
				"format":        format,
				"intervalMs":    args.IntervalMs,
				queryPayloadKey: args.Query,
				"rawQuery":      true,
				"limit":         "",
				"resultFormat":  "time_series",
			},
		},
		"from": strconv.FormatInt(from.UnixMilli(), 10),
		"to":   strconv.FormatInt(to.UnixMilli(), 10),
	}

	fmt.Println(payload)

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
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("InfluxDB query returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read and parse response
	body := io.LimitReader(resp.Body, 1024*1024*60) // 48MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	fmt.Println(len(bodyBytes))
	var queryResp influxQueryResponse
	if err := json.Unmarshal(bodyBytes, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	return &queryResp, nil
}

func enforceQueryLimit(args *InfluxQueryArgs) {
	//flux , influxql limits per measurement(influxql) , table(flux) level so no of measurments * limit is final records
	//sql limit applies on final records level

	limit := InfluxDBDefaultLimit

	if args.Limit >= InfluxDBMaxLimit {
		limit = InfluxDBMaxLimit
	} else if args.Limit > 0 {
		limit = args.Limit
	}

	if args.QueryType == SQLQueryType {
		//wrap query and apply limit
		query := strings.TrimSuffix(args.Query, ";")
		args.Query = "(" + query + ")" + fmt.Sprintf(" LIMIT %d", limit)
	}
	if args.QueryType == InfluxQLQueryType {
		//TODO : apply limit , idea :  from end of string by overriding existing
	}
	if args.QueryType == FluxQueryType {
		//TODO : apply limits for flux query type
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

		//set relative end time 1hour from start
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

	hasResults := false

	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		result.Frames = make([]*InfluxQueryResFrame, 0, len(r.Frames))

		for _, frame := range r.Frames {

			noOfCol := len(frame.Schema.Fields)
			if noOfCol == 0 {
				//no columns for frame , skip frame
				continue
			}

			resFrame := InfluxQueryResFrame{}
			resFrame.Columns = make([]string, 0, noOfCol)

			//no of rows count derived from count of values of first column
			rowCount := (len(frame.Data.Values[0]))
			resFrame.RowCount = uint(rowCount)
			resFrame.Rows = make([]map[string]any, 0, rowCount)
			resFrame.Name = frame.Schema.Name

			for colNo, field := range frame.Schema.Fields {

				fieldName := field.Name

				if field.Labels.Field != "" && field.Name == "_value" {
					//use field name for column values of flux queries
					fieldName = field.Labels.Field
				}

				resFrame.Columns = append(resFrame.Columns, fieldName)

				for rowId, colValue := range frame.Data.Values[colNo] {
					if len(resFrame.Rows) < (rowId + 1) {
						resFrame.Rows = append(resFrame.Rows, make(map[string]any))
					}

					resFrame.Rows[rowId][fieldName] = colValue
				}
			}

			result.Frames = append(result.Frames, &resFrame)
			if rowCount > 0 && !hasResults {
				hasResults = true
			}
		}
	}

	result.FramesCount = len(result.Frames)

	/*
		InfluxQL Query has a frame for each column selection , ( different selection set result in varying row count for each frame)
		SQL Query results in a single frame , selected columsn are mapped in frame.columns
	*/

	if !hasResults {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: InfluxDBDataSourceType,
			Query:          originalQuery,
			ProcessedQuery: args.Query,
			StartTime:      *from,
			EndTime:        *to,
		})
	}

	return &result, nil
}

var QueryInflux = mcpgrafana.MustTool(
	"query_influx",
	"Queries influxdb of a datasource , supports one of flux , sql , influxql associated with datasource ",
	queryInflux,
	mcp.WithTitleAnnotation("Query InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListBucketArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
}
type ListBucketResult struct {
	Buckets     *[]string         `json:"buckets"`
	BucketCount uint              `json:"bucketCount"`
	Hints       *EmptyResultHints `json:"hints,omitempty"`
}

func extractColValues(resp *influxQueryResponse, colName string) (*[]string, error) {
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
				//no bucket name col found
				continue
			}

			if len(frame.Data.Values) <= fieldColIdx {
				continue
			}

			resizedFieldValues := make([]string, len(fieldValues), len(fieldValues)+len(frame.Data.Values[fieldColIdx]))
			copy(resizedFieldValues, fieldValues)
			fieldValues = resizedFieldValues

			for _, name := range frame.Data.Values[fieldColIdx] {
				fieldValues = append(fieldValues, name.(string))
			}
		}
	}

	return &fieldValues, nil
}

func listBuckets(ctx context.Context, args ListBucketArgs) (*ListBucketResult, error) {
	queryType := FluxQueryType
	client, _, err := newInfluxDBClient(ctx, args.DatasourceUID, &queryType)

	if err != nil {
		pattern := `^datasource \S+ is configured with querytype \S+, not \S+$`

		matched, _ := regexp.MatchString(pattern, err.Error())

		if matched {
			return nil, fmt.Errorf("Datasource is not configured with FluxQL , bucket listing is explicit to FluxQL linked datasources")
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

	if len(*buckets) == 0 {
		//return empty result hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: FluxQueryType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("Empty results , check is buckets exist for connected datasources"),
		})
	}

	result.BucketCount = uint(len(*buckets))
	result.Buckets = buckets
	return &result, nil
}

var ListBucketsInflux = mcpgrafana.MustTool(
	"list_buckets_influxdb",
	"Lists buckets of a InfluxDB Datasource identified with DataSourceId , requires the datasources to be linked with FluxQL , use in order list_datasources -> get_datasources -> list_buckets_influxdb",
	listBuckets,
	mcp.WithTitleAnnotation("List Buckets InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListMeasurementsArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
	Bucket        string `json:"bucket,omitempty" jsonschema:"optional,description=Bucket Name of target bucket to fetch from,only required for FluxQL linked datasources."`
	Limit         uint   `json:"limit"`
}

type ListMeasurementResult struct {
	Measurements     *[]string         `json:"measurements"`
	MeasurementCount uint              `json:"measurementCount"`
	Hints            *EmptyResultHints `json:"hints,omitempty"`
}

func enforeMeasurementsLimit(args *ListMeasurementsArgs) {
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

	enforeMeasurementsLimit(&args)

	if queryType == FluxQueryType && args.Bucket == "" {
		return nil, fmt.Errorf("Bucket is required for %s linked InfluxDb Datasources", FluxQueryType)
	}
	var query string
	//represents column key of measurment in response
	var colKey string
	switch queryType {
	case SQLQueryType:
		query = fmt.Sprintf("SELECT table_name FROM information_schema.tables WHERE table_schema = 'iox' ORDER BY table_name LIMIT %d", args.Limit)
		colKey = "table_name"
	case FluxQueryType:
		query = fmt.Sprintf(`import "influxdata/influxdb/schema" schema.measurements(bucket: "%s")|> limit(n: %d)`, args.Bucket, args.Limit)
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

	if len(*measurements) == 0 {
		//add empty results hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: FluxQueryType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("No measurements found , verify at datasource"),
		})
	}

	result.MeasurementCount = uint(len(*measurements))
	result.Measurements = measurements
	return &result, nil
}

var ListMeasurements = mcpgrafana.MustTool(
	"list_measurements_influxdb",
	"Lists Measurments of a InfluxDB Datasource identified with DataSourceId , use in order list_datasources -> get_datasources -> list_buckets_influxdb(only for fluxql linked datasource) -> list_measurements_influxdb",
	listMeasurements,
	mcp.WithTitleAnnotation("List Measurements InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListTagKeysArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
	Bucket        string `json:"bucket,omitempty" jsonschema:"optional,description=Bucket Name of target bucket to fetch from,only required for FluxQL linked datasources."`
	Measurement   string `json:"measurement" jsonschema:"required,description=Filter by measurement"`
	Limit         uint   `json:"limit"`
}
type ListTagKeysResult struct {
	TagKeys      *[]string         `json:"tags"`
	TagKeysCount uint              `json:"tagCount"`
	Hints        *EmptyResultHints `json:"hints,omitempty"`
}

func enforeTagKeysLimit(args *ListTagKeysArgs) {
	if args.Limit > InfluxDbTagsMaxLimit {
		args.Limit = InfluxDbTagsMaxLimit
	}
	if args.Limit == 0 {
		args.Limit = InfluxDbTagsDefaultLimit
	}
}

func listTagKeys(ctx context.Context, args ListTagKeysArgs) (*ListTagKeysResult, error) {
	enforeTagKeysLimit(&args)

	client, queryType, err := newInfluxDBClient(ctx, args.DatasourceUID, nil)

	if err != nil {
		return nil, err
	}

	var tagColumnKey string
	var query string

	switch queryType {
	case SQLQueryType:
		//TODO : Escape '-' for measurement name 
		//data_type 'Dictionary%%' distiguishes tags from fields for SQL QURIES
		query = fmt.Sprintf(`SELECT column_name FROM information_schema.columns WHERE table_schema = 'iox' AND table_name = '%s' AND data_type LIKE 'Dictionary%%' ORDER BY column_name LIMIT %d`, args.Bucket, args.Limit)
		tagColumnKey = "column_name"
	case FluxQueryType:
		query = fmt.Sprintf(`import "influxdata/influxdb/schema" schema.measurementTagKeys(bucket: "%s", measurement: "%s")|> limit(n: %d)`, args.Bucket, args.Measurement, args.Limit)
		tagColumnKey = "_value"
	case InfluxQLQueryType:
		query = fmt.Sprintf(`SHOW TAG KEYS FROM %s LIMIT %d`, args.Measurement, args.Limit)
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

	if len(*tags) == 0 {
		//add empty results hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: FluxQueryType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("No tags found , verify at datasource"),
		})
	}

	result.TagKeysCount = uint(len(*tags))
	result.TagKeys = tags
	return &result, nil
}

var ListTagKeys = mcpgrafana.MustTool(
	"list_tag_keys_influxdb",
	"Lists Tag Keys of a InfluxDB Datasource identified with DataSourceId , use in order list_datasources -> get_datasources -> list_buckets_influxdb -> list_measurements_influxdb -> list_tag_keys_influxdb",
	listTagKeys,
	mcp.WithTitleAnnotation("List Tag Keys InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListFieldKeysArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource. Use list_datasources to find available UIDs."`
	Bucket        string `json:"bucket,omitempty" jsonschema:"optional,description=Bucket Name of target bucket to fetch from,only required for FluxQL linked datasources."`
	Measurement   string `json:"measurement" jsonschema:"required,description=Filter by measurement"`
	Limit         uint   `json:"limit"`
}

type ListFieldKeysResult struct {
	FieldKeys      *[]string         `json:"fields"`
	FieldKeysCount uint              `json:"fieldCount"`
	Hints          *EmptyResultHints `json:"hints,omitempty"`
}

// field keys, tag key use same variable for limits
func enforeFieldKeysLimit(args *ListFieldKeysArgs) {
	if args.Limit > InfluxDbTagsMaxLimit {
		args.Limit = InfluxDbTagsMaxLimit
	}
	if args.Limit == 0 {
		args.Limit = InfluxDbTagsDefaultLimit
	}
}

func listFieldKeys(ctx context.Context, args ListFieldKeysArgs) (*ListFieldKeysResult, error) {
	enforeFieldKeysLimit(&args)

	client, queryType, err := newInfluxDBClient(ctx, args.DatasourceUID, nil)

	if err != nil {
		return nil, err
	}

	var fieldColumnKey string
	var query string

	switch queryType {
	case SQLQueryType:
		//data_type 'Dictionary%%' distiguishes tags from fields for SQL QURIES
		query = fmt.Sprintf("SELECT column_name FROM information_schema.columns WHERE table_schema = 'iox' AND table_name = '%s' AND data_type NOT LIKE 'Dictionary%%' ORDER BY column_name LIMIT %d", args.Measurement, args.Limit)
		fieldColumnKey = "column_name"
	case FluxQueryType:
		query = fmt.Sprintf(`import "influxdata/influxdb/schema" schema.measurementFieldKeys(bucket: "%s", measurement: "%s")|> limit(n: %d)`, args.Bucket, args.Measurement, args.Limit)
		fieldColumnKey = "_value"
	case InfluxQLQueryType:
		query = fmt.Sprintf(`SHOW FIELD KEYS FROM %s LIMIT %d`, args.Measurement, args.Limit)
		fieldColumnKey = "Value"
	}

	refTime := time.Now()
	response, err := client.Query(ctx, InfluxQueryArgs{DatasourceUID: args.DatasourceUID, Query: query, QueryType: queryType, Start: "", End: ""}, refTime, refTime)

	if err != nil {
		return nil, err
	}

	tags, err := extractColValues(response, fieldColumnKey)

	if err != nil {
		return nil, err
	}

	result := ListFieldKeysResult{}

	if len(*tags) == 0 {
		//add empty results hints
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: FluxQueryType,
			Query:          query,
			ProcessedQuery: query,
			StartTime:      refTime,
			EndTime:        refTime,
			Error:          fmt.Errorf("No tags found , verify at datasource"),
		})
	}

	result.FieldKeysCount = uint(len(*tags))
	result.FieldKeys = tags
	return &result, nil
}

var ListFieldKeys = mcpgrafana.MustTool(
	"list_field_keys_influxdb",
	"Lists Field Keys of a InfluxDB Datasource identified with DataSourceId , use in order list_datasources -> get_datasources -> list_buckets_influxdb -> list_measurements_influxdb -> list_field_keys_influxdb",
	listFieldKeys,
	mcp.WithTitleAnnotation("List Field Keys InfluxDB"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)


func AddInfluxTools(mcp *server.MCPServer) {
	QueryInflux.Register(mcp)
	ListBucketsInflux.Register(mcp)
	ListMeasurements.Register(mcp)
	ListTagKeys.Register(mcp)
	ListFieldKeys.Register(mcp)
}
