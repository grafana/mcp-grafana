package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
}

// LabelResponse represents the http json response to a label query
type LabelResponse struct {
	Status string   `json:"status"`
	Data   []string `json:"data,omitempty"`
}

// Stats represents the statistics returned by Loki's index/stats endpoint
type Stats struct {
	Streams int `json:"streams"`
	Chunks  int `json:"chunks"`
	Entries int `json:"entries"`
	Bytes   int `json:"bytes"`
}

func newLokiClient(ctx context.Context, uid string) (*Client, error) {
	grafanaURL, apiKey := mcpgrafana.GrafanaURLFromContext(ctx), mcpgrafana.GrafanaAPIKeyFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(grafanaURL, "/"), uid)

	client := &http.Client{
		Transport: &authRoundTripper{
			apiKey:     apiKey,
			underlying: http.DefaultTransport,
		},
	}

	return &Client{
		httpClient: client,
		baseURL:    url,
	}, nil
}

// buildURL constructs a full URL for a Loki API endpoint
func (c *Client) buildURL(urlPath string) string {
	fullURL := c.baseURL
	if !strings.HasSuffix(fullURL, "/") && !strings.HasPrefix(urlPath, "/") {
		fullURL += "/"
	} else if strings.HasSuffix(fullURL, "/") && strings.HasPrefix(urlPath, "/") {
		// Remove the leading slash from urlPath to avoid double slash
		urlPath = strings.TrimPrefix(urlPath, "/")
	}
	return fullURL + urlPath
}

// makeRequest makes an HTTP request to the Loki API and returns the response body
func (c *Client) makeRequest(ctx context.Context, method, urlPath string, params url.Values) ([]byte, error) {
	fullURL := c.buildURL(urlPath)

	u, err := url.Parse(fullURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}

	if params != nil {
		u.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	// Check for non-200 status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Loki API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read the response body with a limit to prevent memory issues
	body := io.LimitReader(resp.Body, 1024*1024*48)
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Check if the response is empty
	if len(bodyBytes) == 0 {
		return nil, fmt.Errorf("empty response from Loki API")
	}

	// Trim any whitespace that might cause JSON parsing issues
	return bytes.TrimSpace(bodyBytes), nil
}

// fetchData is a generic method to fetch data from Loki API
func (c *Client) fetchData(ctx context.Context, urlPath string, startRFC3339, endRFC3339 string) ([]string, error) {
	params := url.Values{}
	if startRFC3339 != "" {
		params.Add("start", startRFC3339)
	}
	if endRFC3339 != "" {
		params.Add("end", endRFC3339)
	}

	bodyBytes, err := c.makeRequest(ctx, "GET", urlPath, params)
	if err != nil {
		return nil, err
	}

	var labelResponse LabelResponse
	err = json.Unmarshal(bodyBytes, &labelResponse)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling response (content: %s): %w", string(bodyBytes), err)
	}

	if labelResponse.Status != "success" {
		return nil, fmt.Errorf("Loki API returned unexpected response format: %s", string(bodyBytes))
	}

	// Check if Data is nil or empty and handle it explicitly
	if labelResponse.Data == nil {
		// Return empty slice instead of nil to avoid potential nil pointer issues
		return []string{}, nil
	}

	if len(labelResponse.Data) == 0 {
		return []string{}, nil
	}

	return labelResponse.Data, nil
}

type authRoundTripper struct {
	apiKey     string
	underlying http.RoundTripper
}

func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+rt.apiKey)
	}

	resp, err := rt.underlying.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// ListLokiLabelNamesParams defines the parameters for listing Loki label names
type ListLokiLabelNamesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally, the start time of the time range to filter the results by"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally, the end time of the time range to filter the results by"`
}

// listLokiLabelNames lists all label names in a Loki datasource
func listLokiLabelNames(ctx context.Context, args ListLokiLabelNamesParams) ([]string, error) {
	client, err := newLokiClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki client: %w", err)
	}

	result, err := client.fetchData(ctx, "/loki/api/v1/labels", args.StartRFC3339, args.EndRFC3339)
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return []string{}, nil
	}

	return result, nil
}

// ListLokiLabelNames is a tool for listing Loki label names
var ListLokiLabelNames = mcpgrafana.MustTool(
	"list_loki_label_names",
	"List the label names in a Loki datasource",
	listLokiLabelNames,
)

// ListLokiLabelValuesParams defines the parameters for listing Loki label values
type ListLokiLabelValuesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LabelName     string `json:"labelName" jsonschema:"required,description=The name of the label to query"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally, the start time of the query"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally, the end time of the query"`
}

// listLokiLabelValues lists all values for a specific label in a Loki datasource
func listLokiLabelValues(ctx context.Context, args ListLokiLabelValuesParams) ([]string, error) {
	client, err := newLokiClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki client: %w", err)
	}

	// Use the client's fetchData method
	urlPath := fmt.Sprintf("/loki/api/v1/label/%s/values", args.LabelName)

	result, err := client.fetchData(ctx, urlPath, args.StartRFC3339, args.EndRFC3339)
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		// Return empty slice instead of nil
		return []string{}, nil
	}

	return result, nil
}

// ListLokiLabelValues is a tool for listing Loki label values
var ListLokiLabelValues = mcpgrafana.MustTool(
	"list_loki_label_values",
	"Get the values of a label in Loki",
	listLokiLabelValues,
)

// fetchStats is a method to fetch stats data from Loki API
func (c *Client) fetchStats(ctx context.Context, query, startRFC3339, endRFC3339 string) (*Stats, error) {
	params := url.Values{}
	params.Add("query", query)

	// Convert RFC3339 timestamps to Unix nanoseconds if provided
	if startRFC3339 != "" {
		startTime, err := time.Parse(time.RFC3339, startRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		params.Add("start", fmt.Sprintf("%d", startTime.UnixNano()))
	}

	if endRFC3339 != "" {
		endTime, err := time.Parse(time.RFC3339, endRFC3339)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		params.Add("end", fmt.Sprintf("%d", endTime.UnixNano()))
	}

	bodyBytes, err := c.makeRequest(ctx, "GET", "/loki/api/v1/index/stats", params)
	if err != nil {
		return nil, err
	}

	var stats Stats
	err = json.Unmarshal(bodyBytes, &stats)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling response (content: %s): %w", string(bodyBytes), err)
	}

	return &stats, nil
}

// QueryLokiStatsParams defines the parameters for querying Loki stats
type QueryLokiStatsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the datasource to query"`
	LogQL         string `json:"logql" jsonschema:"required,description=The LogQL query to execute"`
	StartRFC3339  string `json:"startRfc3339,omitempty" jsonschema:"description=Optionally, the start time of the query in RFC3339 format"`
	EndRFC3339    string `json:"endRfc3339,omitempty" jsonschema:"description=Optionally, the end time of the query in RFC3339 format"`
}

// queryLokiStats queries stats from a Loki datasource using LogQL
func queryLokiStats(ctx context.Context, args QueryLokiStatsParams) (*Stats, error) {
	client, err := newLokiClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Loki client: %w", err)
	}

	// Set default time range if not provided
	startTime := args.StartRFC3339
	endTime := args.EndRFC3339
	if startTime == "" {
		// Default to 1 hour ago if not specified
		startTime = time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	}
	if endTime == "" {
		// Default to now if not specified
		endTime = time.Now().Format(time.RFC3339)
	}

	stats, err := client.fetchStats(ctx, args.LogQL, startTime, endTime)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// QueryLokiStats is a tool for querying stats from Loki
var QueryLokiStats = mcpgrafana.MustTool(
	"query_loki_stats",
	"Query statistics about logs in a Loki datasource using LogQL",
	queryLokiStats,
)

// AddLokiTools registers all Loki tools with the MCP server
func AddLokiTools(mcp *server.MCPServer) {
	ListLokiLabelNames.Register(mcp)
	ListLokiLabelValues.Register(mcp)
	QueryLokiStats.Register(mcp)
}
