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

// fetchData is a generic method to fetch data from Loki API
func (c *Client) fetchData(ctx context.Context, urlPath string, startRFC3339, endRFC3339 string) ([]string, error) {
	var data []string

	fullURL := c.baseURL
	if !strings.HasSuffix(fullURL, "/") && !strings.HasPrefix(urlPath, "/") {
		fullURL += "/"
	} else if strings.HasSuffix(fullURL, "/") && strings.HasPrefix(urlPath, "/") {
		// Remove the leading slash from urlPath to avoid double slash
		urlPath = strings.TrimPrefix(urlPath, "/")
	}
	fullURL += urlPath

	u, err := url.Parse(fullURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}

	params := url.Values{}
	if startRFC3339 != "" {
		params.Add("start", startRFC3339)
	}
	if endRFC3339 != "" {
		params.Add("end", endRFC3339)
	}

	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return data, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return data, fmt.Errorf("executing request: %w", err)
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
		return data, fmt.Errorf("reading response body: %w", err)
	}

	// Check if the response is empty
	if len(bodyBytes) == 0 {
		return data, fmt.Errorf("empty response from Loki API")
	}

	// Trim any whitespace that might cause JSON parsing issues
	trimmedBody := bytes.TrimSpace(bodyBytes)

	var labelResponse LabelResponse
	err = json.Unmarshal(trimmedBody, &labelResponse)
	if err != nil {
		return data, fmt.Errorf("unmarshalling response (content: %s): %w", string(trimmedBody), err)
	}

	if labelResponse.Status != "success" {
		return data, fmt.Errorf("Loki API returned unexpected response format: %s", string(trimmedBody))
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

	// Handle empty results explicitly to avoid Zod validation errors
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

	// Handle empty results explicitly to avoid Zod validation errors
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

// AddLokiTools registers all Loki tools with the MCP server
func AddLokiTools(mcp *server.MCPServer) {
	ListLokiLabelNames.Register(mcp)
	ListLokiLabelValues.Register(mcp)
}
