package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/grafana/mcp-grafana/auth"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	// networkQuietDuration is how long the network must be idle before we consider the page loaded.
	networkQuietDuration = 2 * time.Second
	// minWaitAfterNav is the minimum time to wait after navigation before checking network idle.
	minWaitAfterNav = 1 * time.Second
	// defaultRenderTimeout is the default max time to wait for rendering.
	defaultRenderTimeout = 60 * time.Second
)

type RenderPanelLocalParams struct {
	DashboardUID  string            `json:"dashboardUid" jsonschema:"required,description=The UID of the dashboard to render"`
	PanelID       *int              `json:"panelId,omitempty" jsonschema:"description=The ID of the panel to render. If omitted\\, the entire dashboard is rendered"`
	DatasourceUID *string           `json:"datasourceUid,omitempty" jsonschema:"description=Datasource UID. When provided\\, auto-resolves the datasource template variable."`
	Width         *int              `json:"width,omitempty" jsonschema:"description=Width of the rendered image in pixels. Defaults to 1000"`
	Height        *int              `json:"height,omitempty" jsonschema:"description=Height of the rendered image in pixels. Defaults to 500"`
	TimeRange     *RenderTimeRange  `json:"timeRange,omitempty" jsonschema:"description=Time range for the rendered image"`
	Variables     map[string]string `json:"variables,omitempty" jsonschema:"description=Dashboard variables to apply (e.g.\\, {\"var-namespace\": \"my-tenant\"})"`
	Theme         *string           `json:"theme,omitempty" jsonschema:"description=Theme for the rendered image: light or dark. Defaults to dark"`
	Scale         *int              `json:"scale,omitempty" jsonschema:"description=Scale factor for the image (1-3). Defaults to 1"`
	Timeout       *int              `json:"timeout,omitempty" jsonschema:"description=Rendering timeout in seconds. Defaults to 60"`
}

func renderPanelImageLocal(ctx context.Context, args RenderPanelLocalParams) (*mcp.CallToolResult, error) {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(config.URL, "/")

	if baseURL == "" {
		return nil, fmt.Errorf("grafana URL not configured. Please set GRAFANA_URL environment variable")
	}

	// Load session cookie
	cookie, err := loadSessionCookie(baseURL, config.BrowserAuth)
	if err != nil {
		return nil, err
	}

	// Auto-resolve datasource variable if provided
	if args.DatasourceUID != nil {
		args.Variables, err = resolveDatasourceVariable(ctx, args.DashboardUID, *args.DatasourceUID, args.Variables)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve datasource variable: %w", err)
		}
	}

	// Build the dashboard URL (not /render/)
	dashURL, err := buildLocalRenderURL(baseURL, args)
	if err != nil {
		return nil, fmt.Errorf("failed to build dashboard URL: %w", err)
	}

	// Determine dimensions and scale
	width := 1000
	height := 500
	scale := 1
	if args.Width != nil {
		width = *args.Width
	}
	if args.Height != nil {
		height = *args.Height
	}
	if args.Scale != nil && *args.Scale >= 1 && *args.Scale <= 3 {
		scale = *args.Scale
	}

	// Determine timeout
	timeout := defaultRenderTimeout
	if args.Timeout != nil && *args.Timeout > 0 {
		timeout = time.Duration(*args.Timeout) * time.Second
	}

	// Parse domain for cookie injection
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Grafana URL: %w", err)
	}

	// Render with headless Chrome
	imageData, err := renderWithChrome(ctx, dashURL, cookie, parsedURL.Hostname(), width, height, scale, timeout)
	if err != nil {
		return nil, fmt.Errorf("local rendering failed: %w", err)
	}

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

// loadSessionCookie loads the grafana_session cookie from the session store,
// triggering a browser login if needed and browser auth is enabled.
func loadSessionCookie(grafanaURL string, browserAuthEnabled bool) (string, error) {
	store := auth.NewSessionStore()
	cookie := store.Load(grafanaURL)
	if cookie != "" {
		return cookie, nil
	}

	if !browserAuthEnabled {
		return "", fmt.Errorf("no session cookie available and browser auth is not enabled; start with --browser-auth flag")
	}

	slog.Info("No session cookie found, triggering browser login")
	newCookie, err := auth.BrowserLogin(grafanaURL)
	if err != nil {
		return "", fmt.Errorf("browser login failed: %w", err)
	}

	if err := store.Save(grafanaURL, newCookie); err != nil {
		slog.Warn("Failed to save session cookie", "error", err)
	}

	return newCookie, nil
}

// buildLocalRenderURL builds the actual dashboard URL for local rendering.
// Uses the real dashboard path (not /render/) with kiosk mode and refresh disabled.
func buildLocalRenderURL(baseURL string, args RenderPanelLocalParams) (string, error) {
	baseURL = strings.TrimRight(baseURL, "/")

	// Build path
	dashPath := fmt.Sprintf("/d/%s", args.DashboardUID)

	params := url.Values{}

	// Panel isolation
	if args.PanelID != nil {
		params.Set("viewPanel", strconv.Itoa(*args.PanelID))
	}

	// Time range
	if args.TimeRange != nil {
		if args.TimeRange.From != "" {
			params.Set("from", args.TimeRange.From)
		}
		if args.TimeRange.To != "" {
			params.Set("to", args.TimeRange.To)
		}
	}

	// Theme
	if args.Theme != nil {
		params.Set("theme", *args.Theme)
	}

	// Dashboard variables
	for key, value := range args.Variables {
		params.Set(key, value)
	}

	// Kiosk mode for clean rendering (hides nav bar, sidebars)
	params.Set("kiosk", "")

	// Disable auto-refresh to prevent infinite network activity
	params.Set("refresh", "")

	return fmt.Sprintf("%s%s?%s", baseURL, dashPath, params.Encode()), nil
}

// renderWithChrome launches a headless Chrome instance, navigates to the URL,
// waits for all network requests to complete (network idle), and takes a screenshot.
func renderWithChrome(ctx context.Context, targetURL, cookie, domain string, width, height, scale int, timeout time.Duration) ([]byte, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(width, height),
		chromedp.Flag("force-device-scale-factor", strconv.Itoa(scale)),
		chromedp.Flag("hide-scrollbars", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(browserCtx, timeout)
	defer timeoutCancel()

	var screenshotBuf []byte

	err := chromedp.Run(timeoutCtx,
		// Inject session cookie before navigation
		network.Enable(),
		setCookieAction(cookie, domain),

		// Navigate to dashboard
		chromedp.Navigate(targetURL),

		// Wait for network idle (all HTTP requests completed + quiet period)
		waitForNetworkIdle(timeoutCtx, networkQuietDuration, minWaitAfterNav),

		// Take screenshot
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			screenshotBuf, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				WithCaptureBeyondViewport(false).
				Do(ctx)
			return err
		}),
	)
	if err != nil {
		return nil, err
	}

	return screenshotBuf, nil
}

// setCookieAction creates a chromedp action that sets the grafana_session cookie.
func setCookieAction(cookie, domain string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return network.SetCookie("grafana_session", cookie).
			WithDomain(domain).
			WithPath("/").
			WithHTTPOnly(true).
			Do(ctx)
	})
}

// waitForNetworkIdle waits until all in-flight HTTP requests have completed
// and no new requests start for the specified quiet duration.
// This is more reliable than CSS selector polling because it's Grafana-version-agnostic.
func waitForNetworkIdle(ctx context.Context, quietDuration, minWait time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(actionCtx context.Context) error {
		var mu sync.Mutex
		pending := make(map[network.RequestID]bool)
		lastActivity := time.Now()

		// Listen for network events
		chromedp.ListenTarget(actionCtx, func(ev interface{}) {
			mu.Lock()
			defer mu.Unlock()

			switch e := ev.(type) {
			case *network.EventRequestWillBeSent:
				pending[e.RequestID] = true
				lastActivity = time.Now()
			case *network.EventLoadingFinished:
				delete(pending, e.RequestID)
				lastActivity = time.Now()
			case *network.EventLoadingFailed:
				delete(pending, e.RequestID)
				lastActivity = time.Now()
			}
		})

		// Wait minimum time after navigation for Grafana to start its data queries
		time.Sleep(minWait)

		// Poll until network is idle
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return fmt.Errorf("timeout waiting for network idle: %w", ctx.Err())
			case <-ticker.C:
				mu.Lock()
				pendingCount := len(pending)
				timeSinceActivity := time.Since(lastActivity)
				mu.Unlock()

				if pendingCount == 0 && timeSinceActivity >= quietDuration {
					slog.Debug("Network idle detected",
						"quiet_for", timeSinceActivity.Round(time.Millisecond),
						"pending_requests", pendingCount,
					)
					return nil
				}
			}
		}
	})
}

var RenderPanelImageLocal = mcpgrafana.MustTool(
	"render_panel_image_local",
	"Render a Grafana dashboard panel as a PNG image using local headless Chrome. Does NOT require the Grafana Image Renderer plugin. Uses browser SSO session for authentication. Supports datasourceUid for auto-resolving the datasource template variable.",
	renderPanelImageLocal,
	mcp.WithTitleAnnotation("Render panel image (local browser)"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
