package sql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/pkg/auth"
)

type SQLDataSource interface {
	Type() string
	GetDatabaseQuery() string
	GetTablesQuery(dbName string) string
	GetSchemaQuery(tableName string, dbName string) string
	//query with limits , indicating wheather limit has been applied
	QueryWithLimit(query string, limit uint) (string, bool)
}

// SQL Implements sql engine execute
type sqlEngine struct {
	grafanaAPIBaseURL string
	grafanaClient     http.Client
}

type BuildQueryArgs struct {
	RefID         string
	DatasourceUId string
	Query         string
	DB            *SQLDataSource
	IntervalMs    uint //optional : not applicable for meta queries
}

type SQLQuery map[string]any

func (*sqlEngine) BuildQuery(args BuildQueryArgs) SQLQuery {
	ds := map[string]any{
		"uid":  args.DatasourceUId,
		"type": (*args.DB).Type(),
	}

	return map[string]any{
		"refId":      args.RefID,
		"datasource": ds,
		"rawSql":     args.Query,
		"format":     "table", //time_series ca
		"intervalMs": args.IntervalMs,
	}
}

type QueryBatchArgs struct {
	From time.Time
	To   time.Time
}

// DSQueryResponse represents the raw API response from Grafana's /api/ds/query
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

type JsonFrame struct {
	Name     string           `json:"name"`
	Columns  []string         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	RowCount uint             `json:"rowCount"`
}

type JsonObject struct {
	Status int          `json:"status,omitempty"`
	Error  string       `json:"error,omitempty"`
	Frames []*JsonFrame `json:"frames"`
}

type SQLQueryResult struct {
	*JsonObject
}

type SQLQueryBatchResult struct {
	Results    map[string]*SQLQueryResult `json:"results"`
	ErrorCount uint
}

// executes gives
func (en *sqlEngine) QueryBatch(ctx context.Context, queries []SQLQuery, args QueryBatchArgs) (*SQLQueryBatchResult, error) {
	payload := map[string]interface{}{
		"queries": queries,
		"from":    strconv.FormatInt(args.From.UnixMilli(), 10),
		"to":      strconv.FormatInt(args.To.UnixMilli(), 10),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	url := en.grafanaAPIBaseURL + "/api/ds/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := en.grafanaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sql query returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read and parse response
	body := io.LimitReader(resp.Body, 1024*1024*48) // 48MB limit
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var response DSQueryResponse
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	result := SQLQueryBatchResult{
		Results: make(map[string]*SQLQueryResult),
	}

	for refID, r := range response.Results {

		frames := make([]*JsonFrame, 0, len(r.Frames))

		for _, frame := range r.Frames {

			noOfCol := len(frame.Schema.Fields)
			if noOfCol == 0 {
				//columns not found for frame, skip frame
				continue
			}

			if len(frame.Data.Values) == 0 {
				//len(frame.Data.Values) equals len(frame.Schema.Fields)
				//this case shoudn't occur
				continue
			}

			//Number of rows count derived from count of values of first column
			noOfRows := (len(frame.Data.Values[0]))

			resFrame := JsonFrame{}
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
			&JsonObject{
				Status: r.Status,
				Error:  r.Error,
				Frames: frames,
			},
		}
		if r.Error != "" {
			result.ErrorCount++
		}

	}

	return &result, nil
}

func NewSQLEngine(ctx context.Context) (*sqlEngine, error) {
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

	transport = auth.NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	httpClient := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	return &sqlEngine{
		grafanaAPIBaseURL: baseURL,
		grafanaClient:     *httpClient,
	}, nil
}
