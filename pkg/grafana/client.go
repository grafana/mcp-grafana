package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/pkg/auth"
)

// GrafanaClient is a http client to make raw http requests to grafana instance
type GrafanaClient struct {
	httpClient *http.Client
	URL        string
}

// NewGrafanaClient creates a new instance of GrafanaClient with Authentication configured from context config
func NewGrafanaClient(ctx context.Context) (*GrafanaClient, error) {
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

	return &GrafanaClient{
		httpClient: httpClient,
		URL:        baseURL,
	}, nil
}

// Post performs a post request
// result should be pointer to the expected response type
//
// Content-Type supports only application/json which is default
//
// Response is limited to 48MB
func (c *GrafanaClient) Post(ctx context.Context, url string, body any, result any) error {
	payloadBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling query payload: %w", err)
	}

	url = c.URL + url
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("response status is not ok, returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read and parse response
	resBody := io.LimitReader(resp.Body, 1024*1024*48) // 48MB limit
	bodyBytes, err := io.ReadAll(resBody)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}
	// TODO : apply relavant error message to reflect memory limit error
	// once https://github.com/grafana/mcp-grafana/pull/622 is merged
	if err := json.Unmarshal(bodyBytes, result); err != nil {
		return fmt.Errorf("unmarshaling response: %w", err)
	}
	return nil
}
