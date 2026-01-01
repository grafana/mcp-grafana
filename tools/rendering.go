package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

type GetPanelImageParams struct {
	DashboardUID string            `json:"dashboardUid" jsonschema:"required,description=The UID of the dashboard containing the panel"`
	PanelID      *int              `json:"panelId,omitempty" jsonschema:"description=The ID of the panel to render. If omitted\\, the entire dashboard is rendered"`
	Width        *int              `json:"width,omitempty" jsonschema:"description=Width of the rendered image in pixels. Defaults to 1000"`
	Height       *int              `json:"height,omitempty" jsonschema:"description=Height of the rendered image in pixels. Defaults to 500"`
	TimeRange    *RenderTimeRange  `json:"timeRange,omitempty" jsonschema:"description=Time range for the rendered image"`
	Variables    map[string]string `json:"variables,omitempty" jsonschema:"description=Dashboard variables to apply (e.g.\\, {\"var-datasource\": \"prometheus\"})"`
	Theme        *string           `json:"theme,omitempty" jsonschema:"description=Theme for the rendered image: light or dark. Defaults to dark"`
	Scale        *int              `json:"scale,omitempty" jsonschema:"description=Scale factor for the image (1-3). Defaults to 1"`
	Timeout      *int              `json:"timeout,omitempty" jsonschema:"description=Rendering timeout in seconds. Defaults to 60"`
}

type RenderTimeRange struct {
	From string `json:"from" jsonschema:"description=Start time (e.g.\\, 'now-1h'\\, '2024-01-01T00:00:00Z')"`
	To   string `json:"to" jsonschema:"description=End time (e.g.\\, 'now'\\, '2024-01-01T12:00:00Z')"`
}

func getPanelImage(ctx context.Context, args GetPanelImageParams) (*mcp.CallToolResult, error) {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(config.URL, "/")

	if baseURL == "" {
		return nil, fmt.Errorf("grafana URL not configured. Please set GRAFANA_URL environment variable or X-Grafana-URL header")
	}

	// Build the render URL
	renderURL, err := buildRenderURL(baseURL, args)
	if err != nil {
		return nil, fmt.Errorf("failed to build render URL: %w", err)
	}

	// Create HTTP client with TLS configuration if available
	httpClient, err := createHTTPClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Set timeout for rendering
	timeout := 60 * time.Second
	if args.Timeout != nil && *args.Timeout > 0 {
		timeout = time.Duration(*args.Timeout) * time.Second
	}
	httpClient.Timeout = timeout

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, renderURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication headers
	if config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.APIKey)
	} else if config.BasicAuth != nil {
		password, _ := config.BasicAuth.Password()
		req.SetBasicAuth(config.BasicAuth.Username(), password)
	}

	// Add org ID header if specified
	if config.OrgID > 0 {
		req.Header.Set("X-Grafana-Org-Id", strconv.FormatInt(config.OrgID, 10))
	}

	// Add user agent
	req.Header.Set("User-Agent", mcpgrafana.UserAgent())

	// Execute request
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch panel image: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("image renderer not available. Ensure the Grafana Image Renderer plugin is installed and configured. See https://grafana.com/docs/grafana/latest/setup-grafana/image-rendering/")
		}
		return nil, fmt.Errorf("failed to render image: HTTP %d - %s", resp.StatusCode, string(body))
	}

	// Read the image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %w", err)
	}

	// Return the image as base64 encoded data using MCP's image content type
	base64Data := base64.StdEncoding.EncodeToString(imageData)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.ImageContent{
				Type:     "image",
				Data:     base64Data,
				MIMEType: "image/png",
			},
		},
	}, nil
}

func buildRenderURL(baseURL string, args GetPanelImageParams) (string, error) {
	// Build the render path
	renderPath := fmt.Sprintf("/render/d/%s", args.DashboardUID)

	// Build query parameters
	params := url.Values{}

	// Set dimensions
	width := 1000
	height := 500
	if args.Width != nil {
		width = *args.Width
	}
	if args.Height != nil {
		height = *args.Height
	}
	params.Set("width", strconv.Itoa(width))
	params.Set("height", strconv.Itoa(height))

	// Set scale
	scale := 1
	if args.Scale != nil && *args.Scale >= 1 && *args.Scale <= 3 {
		scale = *args.Scale
	}
	params.Set("scale", strconv.Itoa(scale))

	// Add panel ID if specified (for single panel rendering)
	if args.PanelID != nil {
		params.Set("viewPanel", strconv.Itoa(*args.PanelID))
	}

	// Add time range
	if args.TimeRange != nil {
		if args.TimeRange.From != "" {
			params.Set("from", args.TimeRange.From)
		}
		if args.TimeRange.To != "" {
			params.Set("to", args.TimeRange.To)
		}
	}

	// Add theme
	if args.Theme != nil {
		params.Set("theme", *args.Theme)
	}

	// Add dashboard variables
	for key, value := range args.Variables {
		params.Set(key, value)
	}

	// Add kiosk mode options for cleaner rendering
	params.Set("kiosk", "true")

	return fmt.Sprintf("%s%s?%s", baseURL, renderPath, params.Encode()), nil
}

func createHTTPClient(config mcpgrafana.GrafanaConfig) (*http.Client, error) {
	client := &http.Client{}

	if config.TLSConfig != nil {
		tlsConfig, err := config.TLSConfig.CreateTLSConfig()
		if err != nil {
			return nil, err
		}
		client.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}

	return client, nil
}

var GetPanelImage = mcpgrafana.MustTool(
	"get_panel_image",
	"Render a Grafana dashboard panel or full dashboard as a PNG image. Returns the image as base64 encoded data. Requires the Grafana Image Renderer plugin to be installed. Use this for generating visual snapshots of dashboards for reports\\, alerts\\, or presentations.",
	getPanelImage,
	mcp.WithTitleAnnotation("Get panel or dashboard image"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddRenderingTools(mcp *server.MCPServer) {
	GetPanelImage.Register(mcp)
}
