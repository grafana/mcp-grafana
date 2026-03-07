package sql

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
		"format":     "table",
		"intervalMs": args.IntervalMs,
	}
}

type QueryBatchArgs struct {
	From time.Time
	To   time.Time
}

// clickHouseQueryResponse represents the raw API response from Grafana's /api/ds/query
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

/*
 reducing the data transfer inbetween
*/

// executes gives
func (en *sqlEngine) QueryBatch(ctx context.Context, queries []SQLQuery, args QueryBatchArgs) (any, error) {
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

	var queryResp DSQueryResponse
	if err := json.Unmarshal(bodyBytes, &queryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	//TODO : implement response formatter

	return &queryResp, nil
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

//pool -> maintain datasource client mapped to type
//central to all datasources
//map[string]any //datasource -> datasource
//pkg -> datasource
