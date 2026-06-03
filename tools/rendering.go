package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// StringOrSlice is a type that can be unmarshaled from either a JSON string
// or an array of strings. This allows dashboard variables to support both
// single-value (e.g., "prometheus") and multi-value (e.g., ["server1", "server2"])
// inputs.
type StringOrSlice []string

// UnmarshalJSON implements the json.Unmarshaler interface.
// It accepts both a JSON string and a JSON array of strings.
func (s *StringOrSlice) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a single string first.
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = StringOrSlice{single}
		return nil
	}

	// Try to unmarshal as an array of strings.
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("variables value must be a string or array of strings, got: %s", string(data))
	}
	*s = StringOrSlice(arr)
	return nil
}

// MarshalJSON implements the json.Marshaler interface.
// A single-element slice is marshaled as a plain string for backward compatibility.
func (s StringOrSlice) MarshalJSON() ([]byte, error) {
	if len(s) == 1 {
		return json.Marshal(s[0])
	}
	return json.Marshal([]string(s))
}

// JSONSchema implements the jsonschema.customSchemaGetterType interface so that
// the schema reflector emits a union type: either a string or an array of strings.
func (StringOrSlice) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			{Type: "string"},
			{Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		},
	}
}

type GetPanelImageParams struct {
	DashboardUID string                   `json:"dashboardUid,omitempty" jsonschema:"description=The UID of a stored dashboard containing the panel. Required unless provisioningPreview is provided."`
	PanelID      *int                     `json:"panelId,omitempty" jsonschema:"description=The ID of the panel to render. If omitted\\, the entire dashboard is rendered"`
	Width        *int                     `json:"width,omitempty" jsonschema:"description=Width of the rendered image in pixels. Defaults to 1000"`
	Height       *int                     `json:"height,omitempty" jsonschema:"description=Height of the rendered image in pixels. Defaults to 500"`
	TimeRange    *RenderTimeRange         `json:"timeRange,omitempty" jsonschema:"description=Time range for the rendered image"`
	Variables    map[string]StringOrSlice `json:"variables,omitempty" jsonschema:"description=Dashboard variables to apply. Values can be a single string or an array of strings for multi-value variables (e.g.\\, {\"var-datasource\": \"prometheus\"\\, \"var-instance\": [\"server1\"\\, \"server2\"]})"`
	Theme        *string                  `json:"theme,omitempty" jsonschema:"description=Theme for the rendered image: light or dark. Defaults to dark"`
	Scale        *int                     `json:"scale,omitempty" jsonschema:"description=Scale factor for the image (1-3). Defaults to 1"`
	Timeout      *int                     `json:"timeout,omitempty" jsonschema:"description=Rendering timeout in seconds. Defaults to 60"`
	// ProvisioningPreview renders a dashboard from a provisioning repository
	// branch that has not yet been merged or applied. Mutually exclusive with
	// dashboardUid.
	ProvisioningPreview *ProvisioningPreview `json:"provisioningPreview,omitempty" jsonschema:"description=Render a dashboard from a provisioning repository branch (e.g. a git-sync PR preview). Mutually exclusive with dashboardUid."`
}

type RenderTimeRange struct {
	From string `json:"from" jsonschema:"description=Start time (e.g.\\, 'now-1h'\\, '2024-01-01T00:00:00Z')"`
	To   string `json:"to" jsonschema:"description=End time (e.g.\\, 'now'\\, '2024-01-01T12:00:00Z')"`
}

// ProvisioningPreview identifies a not-yet-applied dashboard inside a
// provisioning repository, e.g. one staged on a feature branch via git-sync.
// The renderer loads the dashboard at /dashboard/provisioning/<repo>/preview/<path>
// instead of looking it up by UID.
type ProvisioningPreview struct {
	Repo string `json:"repo" jsonschema:"required,description=Provisioning repository slug. List repositories via /apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories if unknown."`
	Path string `json:"path" jsonschema:"required,description=Path to the dashboard file within the repository\\, relative to its root."`
	Ref  string `json:"ref,omitempty" jsonschema:"description=Branch or commit SHA to render from. Defaults to the repository's main branch when omitted."`
}

func getPanelImage(ctx context.Context, args GetPanelImageParams) (*mcp.CallToolResult, error) {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := config.URL

	if baseURL == "" {
		return nil, fmt.Errorf("grafana URL not configured. Please set GRAFANA_URL environment variable or X-Grafana-URL header")
	}

	// Build the render URL
	renderURL, err := buildRenderURL(baseURL, args)
	if err != nil {
		return nil, fmt.Errorf("failed to build render URL: %w", err)
	}

	// Build the HTTP transport using the shared middleware chain (TLS, auth,
	// extra headers, org ID, user-agent, otel). Using config.HTTPTransport()
	// as the base ensures we respect BaseTransport when set (e.g. the hosted
	// Cloud MCP server injects a pre-configured transport with custom
	// instrumentation).
	transport, err := mcpgrafana.BuildTransport(&config, config.HTTPTransport())
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP transport: %w", err)
	}

	timeout := 60 * time.Second
	if args.Timeout != nil && *args.Timeout > 0 {
		timeout = time.Duration(*args.Timeout) * time.Second
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, renderURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Prefer raw image bytes so API gateways (e.g. Kong) that inspect
	// Accept to decide response format return the PNG directly.
	req.Header.Set("Accept", "image/*")

	// Execute request
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch panel image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("image renderer not available. Ensure the Grafana Image Renderer service is installed and configured. See https://grafana.com/docs/grafana/latest/setup-grafana/image-rendering/")
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
	// Validate that exactly one source is set.
	hasUID := args.DashboardUID != ""
	hasPreview := args.ProvisioningPreview != nil
	if hasUID == hasPreview {
		if hasUID {
			return "", fmt.Errorf("dashboardUid and provisioningPreview are mutually exclusive; pass exactly one")
		}
		return "", fmt.Errorf("either dashboardUid or provisioningPreview must be set")
	}
	if hasPreview {
		if err := validateRepoSlug("provisioningPreview.repo", args.ProvisioningPreview.Repo); err != nil {
			return "", err
		}
		if err := validateRepoPath("provisioningPreview.path", args.ProvisioningPreview.Path); err != nil {
			return "", err
		}
	}

	// Strip trailing slashes from base URL for consistent URL construction
	baseURL = strings.TrimRight(baseURL, "/")

	// Build query parameters
	params := url.Values{}

	// Choose render path. For stored dashboards we use the purpose-built /d-solo
	// route for single-panel renders (lighter than loading the full dashboard
	// with viewPanel); full dashboard renders use /d. For provisioning previews
	// the same route is used for both since the preview UI handles ?panelId
	// via the standard kiosk/viewPanel mechanism.
	var renderPath string
	if hasPreview {
		// Repo is a single segment and gets the stricter url.PathEscape (which
		// also encodes sub-delim characters like @, $, &, ;, =, :). For the
		// multi-segment file path we use url.URL.EscapedPath() so structural /
		// separators between segments are preserved while everything else that
		// isn't valid in a URL path is percent-encoded. This matches the
		// encoding done by tools/navigation.go and tools/provisioning.go.
		// Note: we build the path by string-concatenation rather than via
		// `url.URL{Path: ...}` because url.URL would re-escape our PathEscape
		// output (turning %40 into %2540).
		escapedFile := (&url.URL{Path: strings.TrimLeft(args.ProvisioningPreview.Path, "/")}).EscapedPath()
		renderPath = fmt.Sprintf(
			"/render/dashboard/provisioning/%s/preview/%s",
			url.PathEscape(args.ProvisioningPreview.Repo),
			escapedFile,
		)
		if args.ProvisioningPreview.Ref != "" {
			params.Set("ref", args.ProvisioningPreview.Ref)
		}
		if args.PanelID != nil {
			params.Set("viewPanel", strconv.Itoa(*args.PanelID))
		}
	} else {
		renderPath = fmt.Sprintf("/render/d/%s", args.DashboardUID)
		if args.PanelID != nil {
			renderPath = fmt.Sprintf("/render/d-solo/%s", args.DashboardUID)
			params.Set("panelId", strconv.Itoa(*args.PanelID))
		}
	}

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

	// Add dashboard variables (supports multi-value via params.Add)
	for key, values := range args.Variables {
		for _, v := range values {
			params.Add(key, v)
		}
	}

	// Add kiosk mode options for cleaner rendering
	params.Set("kiosk", "true")

	return fmt.Sprintf("%s%s?%s", baseURL, renderPath, params.Encode()), nil
}

var GetPanelImage = mcpgrafana.MustTool(
	"get_panel_image",
	"Render a Grafana dashboard panel or full dashboard as a PNG image. Returns the image as base64 encoded data. Requires the Grafana Image Renderer service to be installed. "+
		"Either dashboardUid (for stored dashboards) or provisioningPreview (for dashboards staged on a provisioning repository branch, e.g. a git-sync PR) must be supplied. "+
		"Use this for generating visual snapshots of dashboards for reports, alerts, or presentations.",
	getPanelImage,
	mcp.WithTitleAnnotation("Get panel or dashboard image"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddRenderingTools(mcp *server.MCPServer) {
	GetPanelImage.Register(mcp)
}
