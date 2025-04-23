package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/server"
)

func newAssertsClient(ctx context.Context) (*Client, error) {
	grafanaURL, apiKey := mcpgrafana.GrafanaURLFromContext(ctx), mcpgrafana.GrafanaAPIKeyFromContext(ctx)
	url := fmt.Sprintf("%s/api/plugins/grafana-asserts-app/resources/asserts/api-server", strings.TrimRight(grafanaURL, "/"))

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

type GetAssertionsParams struct {
	StartTime  int64  `json:"startTime" jsonschema:"description=The start time of the assertion status in RFC3339 format"`
	EndTime    int64  `json:"endTime" jsonschema:"description=The end time of the assertion status in RFC3339 format"`
	EntityType string `json:"entityType" jsonschema:"description=The type of the entity to list"`
	EntityName string `json:"entityName" jsonschema:"description=The name of the entity to list"`
	Env       string `json:"env" jsonschema:"description=The env of the entity to list"`
	Site      string `json:"site" jsonschema:"description=The site of the entity to list"`
	Namespace string `json:"namespace" jsonschema:"description=The namespace of the entity to list"`
}

type Scope struct {
	Env       string `json:"env"`
	Site      string `json:"site"`
	Namespace string `json:"namespace"`	
}

type Entity struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Scope Scope  `json:"scope"`
}

type RequestBody struct {
	StartTime             int64    `json:"startTime"`
	EndTime               int64    `json:"endTime"`
	EntityKeys            []Entity `json:"entityKeys"`
	SuggestionSrcEntities []Entity `json:"suggestionSrcEntities"`
	AlertCategories       []string `json:"alertCategories"`
}
func (c *Client) fetchAssertsData(ctx context.Context, urlPath string, method string, reqBody any) (string, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+urlPath, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil	
}

func getAssertions(ctx context.Context, args GetAssertionsParams) (string, error) {
	client, err := newAssertsClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create Asserts client: %w", err)
	}

	// Create request body
	reqBody := RequestBody{
		StartTime: args.StartTime * 1000,
		EndTime:   args.EndTime * 1000,
		EntityKeys: []Entity{
			{
				Name: args.EntityName,
				Type: args.EntityType,
				Scope: Scope{
					Env:       args.Env,
					Site:      args.Site,
					Namespace: args.Namespace,
				},
			},
		},
		SuggestionSrcEntities: []Entity{},
		AlertCategories:       []string{"saturation", "amend", "anomaly", "failure", "error"},
	}

	data, err := client.fetchAssertsData(ctx, "/v1/assertions/llm-summary", "POST", reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to fetch data: %w", err)
	}

	return data, nil
}

var GetAssertions = mcpgrafana.MustTool(
	"get_assertions",
	"Get assertion summary for a given entity with its type, name, env, site, namespace, and a time range",
	getAssertions,
)

func AddAssertsTools(mcp *server.MCPServer) {
	GetAssertions.Register(mcp)
}

