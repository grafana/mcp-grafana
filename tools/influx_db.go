package tools

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	InfluxDBDataSourceType = "influxdb"

	InfluxResMaxDataPoints = 100
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
func newInfluxDBClient(ctx context.Context, uid string, queryType *string) (*influxDBClient, error) {
	// Verify the datasource exists and is a InfluxDB datasource
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	if ds.Type != InfluxDBDataSourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, InfluxDBDataSourceType)
	}

	if queryType != nil {
		//verify the query lang specified is the one confgured with datasource
		dsQueryType := InfluxQLQueryType

		if jsonMap, ok := ds.JSONData.(map[string]interface{}); ok {
			if dsQT, ok := jsonMap["version"].(string); ok && dsQT != "" {
				dsQueryType = dsQT
			}
		}

		if *queryType != dsQueryType {
			return nil, fmt.Errorf("datasource %s is configured with querytype %s, not %s", uid, dsQueryType, *queryType)
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
			return nil, fmt.Errorf("failed to create custom transport: %w", err)
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
	}, nil
}

type InfluxQueryArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the InfluxDB datasource to query. Use list_datasources to find available UIDs."`
	Query         string `json:"query" jsonschema:"required,description=SQL/Flux/InfluxQL query. Supports SQL macros: $__timeFilter for time filtering\\, $__timeFrom/$__timeTo for millisecond timestamps\\, $__interval for calculated intervals\\, $__dateBin(<column>)/$__dateBinAlias(<column>) to apply date_bin for timestamp columns. Supports Flux macros : v.timeRangeStart\\, v.timeRangeStop\\, v.windowPeriod (Grafana-calculated interval)\\, v.defaultBucket (configured default bucket)\\, v.organization (configured organization)\\."`
	QueryType     string `json:"query_type"` //TODO : enum options
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

func (ic *influxDBClient) Query(ctx context.Context, args InfluxQueryArgs, from, to time.Time) (influxQueryResponse, error) {
	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"datasource": map[string]string{
					"uid":  args.DatasourceUID,
					"type": InfluxDBDataSourceType,
				},
				"refId":         "A",
				"type":          "timeSeriesQuery",
				"intervalMs":    args.IntervalMs,
				"maxDataPoints": args.Limit,
			},
		},
		"from": strconv.FormatInt(from.UnixMilli(), 10),
		"to":   strconv.FormatInt(to.UnixMilli(), 10),
	}

	// ic.httpClient.Post(ic.baseURL + "/api/ds/query" , map[string]any{
	// 	"queries" : map[string]any{
	// 		"refId" : "A",
	// 		"datasource" : {
	// 			"uid" : args.DatasourceUID,
	// 		}
	// 	}
	// })
}

func enforceQueryLimit(args *InfluxQueryArgs) {
	if args.Limit > InfluxResMaxDataPoints {
		args.Limit = InfluxResMaxDataPoints
	}
}

func queryInflux(ctx context.Context, args InfluxQueryArgs) (*influxQueryResponse, error) {
	client, err := newInfluxDBClient(ctx, args.DatasourceUID, &args.QueryType)

	if err != nil {
		return nil, err
	}

	//todo : enforce time range limits

	enforceQueryLimit(&args)

	res, err := client.Query(ctx, args)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

var QueryInflux = mcpgrafana.MustTool(
	"list_loki_label_values",
	"Retrieves all unique values associated with a specific `labelName` within a Loki datasource and time range. Returns a list of string values (e.g., for `labelName=\"env\"`, might return `[\"prod\", \"staging\", \"dev\"]`). Useful for discovering filter options. Defaults to the last hour if the time range is omitted.",
	queryInflux,
	mcp.WithTitleAnnotation("List Loki label values"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

//Query method
//build query and execute
//struct for client ->

/**
  Client Query method

  Request Args (tool params) , Response ,

  tool specification (object)
  -toolname
  -toolhandler

  add tools ,
  list tools method ,
  **/
//create client and reuse
