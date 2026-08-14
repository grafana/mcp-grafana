package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

const (
	defaultExploreWidth     = 1200
	defaultExploreHeight    = 600
	defaultExploreScale     = 1
	defaultExploreCrop      = "exploreVisualization"
	maxExploreWidth         = 4000
	maxExploreHeight        = 4000
	maxExploreTimeout       = 300
	maxExploreQueries       = 8
	maxExploreVariables     = 20
	maxExploreVariableKey   = 128
	maxExploreVariableValue = 256
	maxExploreModelBytes    = 32 * 1024
	maxExploreURLLength     = 100 * 1024
)

type ExploreRenderQuery struct {
	RefID string         `json:"refId,omitempty" jsonschema:"description=Reference ID for the query. Defaults to A\\, B\\, C and so on."`
	Model map[string]any `json:"model" jsonschema:"required,description=Datasource-specific Grafana query model. For Prometheus this includes expr\\, format\\, instant\\, and range."`
}

type ExploreRenderParams struct {
	DatasourceUID  string               `json:"datasourceUid" jsonschema:"required,description=UID of the Grafana datasource"`
	DatasourceType string               `json:"datasourceType,omitempty" jsonschema:"description=Grafana datasource type. If omitted\\, it is resolved from datasourceUid."`
	Queries        []ExploreRenderQuery `json:"queries" jsonschema:"required,description=Datasource-specific Explore query models"`
	TimeRange      RenderTimeRange      `json:"timeRange" jsonschema:"required,description=Explore time range using relative values or epoch milliseconds"`
	Variables      map[string]string    `json:"variables,omitempty" jsonschema:"description=Optional Explore URL variables"`
	Theme          string               `json:"theme,omitempty" jsonschema:"description=Theme for the rendered image: light or dark. Defaults to dark"`
	Width          int                  `json:"width,omitempty" jsonschema:"description=Viewport width in pixels. Defaults to 1200"`
	Height         int                  `json:"height,omitempty" jsonschema:"description=Viewport height in pixels. Defaults to 600"`
	Scale          int                  `json:"scale,omitempty" jsonschema:"description=Device scale factor from 1 to 3. Defaults to 1"`
	TimeoutSeconds int                  `json:"timeoutSeconds,omitempty" jsonschema:"description=Rendering timeout in seconds from 1 to 300. Defaults to 60"`
	OutputPath     string               `json:"outputPath" jsonschema:"required,description=Absolute PNG path below the configured artifact output root"`
	Crop           string               `json:"crop,omitempty" jsonschema:"description=Capture mode: exploreVisualization or viewport. Defaults to exploreVisualization"`
}

type ExploreRenderResult struct {
	Path       string `json:"path"`
	MIMEType   string `json:"mimeType"`
	Bytes      int    `json:"bytes"`
	SHA256     string `json:"sha256"`
	ExploreURL string `json:"exploreUrl"`
	RenderMode string `json:"renderMode"`
}

type explorePane struct {
	Datasource map[string]string `json:"datasource"`
	Queries    []map[string]any  `json:"queries"`
	Range      RenderTimeRange   `json:"range"`
}

func renderExploreImage(ctx context.Context, args ExploreRenderParams) (ExploreRenderResult, error) {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(config.URL, "/")
	if baseURL == "" {
		return ExploreRenderResult{}, fmt.Errorf("grafana URL not configured. Please set GRAFANA_URL environment variable")
	}

	options, err := validateExploreRenderParams(args)
	if err != nil {
		return ExploreRenderResult{}, err
	}
	outputPath, err := validateArtifactPath(config.ArtifactOutputRoot, args.OutputPath)
	if err != nil {
		return ExploreRenderResult{}, err
	}
	datasourceType := args.DatasourceType
	if datasourceType == "" {
		if mcpgrafana.GrafanaClientFromContext(ctx) == nil {
			return ExploreRenderResult{}, fmt.Errorf("datasourceType is required when no Grafana client is configured")
		}
		ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
		if err != nil {
			return ExploreRenderResult{}, fmt.Errorf("resolve datasource type: %w", err)
		}
		if ds == nil || ds.Type == "" {
			return ExploreRenderResult{}, fmt.Errorf("datasource %q has no type", args.DatasourceUID)
		}
		datasourceType = ds.Type
	}

	exploreURL, err := buildExploreRenderURL(baseURL, config.OrgID, datasourceType, args)
	if err != nil {
		return ExploreRenderResult{}, fmt.Errorf("build Explore URL: %w", err)
	}

	cookie, err := loadSessionCookie(baseURL, config.BrowserAuth)
	if err != nil {
		return ExploreRenderResult{}, err
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return ExploreRenderResult{}, fmt.Errorf("invalid Grafana URL: %w", err)
	}

	imageData, err := renderWithChromeActions(ctx, exploreURL, cookie, parsedURL.Hostname(),
		options.width, options.height, options.scale, options.timeout,
		waitForExploreReady(),
		captureExploreScreenshot(options.crop),
	)
	if err != nil {
		return ExploreRenderResult{}, fmt.Errorf("Explore rendering failed: %w", err)
	}
	if len(imageData) == 0 {
		return ExploreRenderResult{}, fmt.Errorf("Explore rendering produced an empty image")
	}

	if err := writeArtifactAtomically(outputPath, imageData); err != nil {
		return ExploreRenderResult{}, fmt.Errorf("write rendered artifact: %w", err)
	}
	sum := sha256.Sum256(imageData)
	return ExploreRenderResult{
		Path:       outputPath,
		MIMEType:   "image/png",
		Bytes:      len(imageData),
		SHA256:     hex.EncodeToString(sum[:]),
		ExploreURL: exploreURL,
		RenderMode: "browser-explore",
	}, nil
}

type exploreRenderOptions struct {
	width, height, scale int
	timeout              time.Duration
	crop                 string
}

func validateExploreRenderParams(args ExploreRenderParams) (exploreRenderOptions, error) {
	if strings.TrimSpace(args.DatasourceUID) == "" {
		return exploreRenderOptions{}, fmt.Errorf("datasourceUid is required")
	}
	if len(args.Queries) == 0 || len(args.Queries) > maxExploreQueries {
		return exploreRenderOptions{}, fmt.Errorf("queries must contain between 1 and %d items", maxExploreQueries)
	}
	if args.TimeRange.From == "" || args.TimeRange.To == "" {
		return exploreRenderOptions{}, fmt.Errorf("timeRange.from and timeRange.to are required")
	}
	if args.OutputPath == "" {
		return exploreRenderOptions{}, fmt.Errorf("outputPath is required")
	}
	theme := args.Theme
	if theme == "" {
		theme = "dark"
	}
	if theme != "dark" && theme != "light" {
		return exploreRenderOptions{}, fmt.Errorf("theme must be light or dark")
	}
	width := args.Width
	if width == 0 {
		width = defaultExploreWidth
	}
	height := args.Height
	if height == 0 {
		height = defaultExploreHeight
	}
	scale := args.Scale
	if scale == 0 {
		scale = defaultExploreScale
	}
	timeout := args.TimeoutSeconds
	if timeout == 0 {
		timeout = int(defaultRenderTimeout / time.Second)
	}
	if width < 200 || width > maxExploreWidth || height < 100 || height > maxExploreHeight {
		return exploreRenderOptions{}, fmt.Errorf("width must be 200-%d and height must be 100-%d", maxExploreWidth, maxExploreHeight)
	}
	if scale < 1 || scale > 3 {
		return exploreRenderOptions{}, fmt.Errorf("scale must be 1-3")
	}
	if timeout < 1 || timeout > maxExploreTimeout {
		return exploreRenderOptions{}, fmt.Errorf("timeoutSeconds must be 1-%d", maxExploreTimeout)
	}
	crop := args.Crop
	if crop == "" {
		crop = defaultExploreCrop
	}
	if crop != "exploreVisualization" && crop != "viewport" {
		return exploreRenderOptions{}, fmt.Errorf("crop must be exploreVisualization or viewport")
	}
	if len(args.Variables) > maxExploreVariables {
		return exploreRenderOptions{}, fmt.Errorf("variables cannot contain more than %d entries", maxExploreVariables)
	}
	for key, value := range args.Variables {
		if len(key) > maxExploreVariableKey || len(value) > maxExploreVariableValue {
			return exploreRenderOptions{}, fmt.Errorf("Explore variable keys must be at most %d bytes and values at most %d bytes", maxExploreVariableKey, maxExploreVariableValue)
		}
	}
	for i, query := range args.Queries {
		if query.Model == nil {
			return exploreRenderOptions{}, fmt.Errorf("queries[%d].model is required", i)
		}
		modelBytes, err := json.Marshal(query.Model)
		if err != nil {
			return exploreRenderOptions{}, fmt.Errorf("marshal queries[%d].model: %w", i, err)
		}
		if len(modelBytes) > maxExploreModelBytes {
			return exploreRenderOptions{}, fmt.Errorf("queries[%d].model exceeds %d bytes", i, maxExploreModelBytes)
		}
	}
	return exploreRenderOptions{
		width: width, height: height, scale: scale,
		timeout: time.Duration(timeout) * time.Second,
		crop:    crop,
	}, nil
}

func buildExploreRenderURL(baseURL string, orgID int64, datasourceType string, args ExploreRenderParams) (string, error) {
	if strings.TrimSpace(datasourceType) == "" {
		return "", fmt.Errorf("datasourceType is required")
	}
	queries := make([]map[string]any, 0, len(args.Queries))
	for i, query := range args.Queries {
		model := make(map[string]any, len(query.Model)+1)
		for key, value := range query.Model {
			model[key] = value
		}
		refID := query.RefID
		if refID == "" {
			refID = string(rune('A' + i))
		}
		model["refId"] = refID
		queries = append(queries, model)
	}
	pane := explorePane{
		Datasource: map[string]string{"type": datasourceType, "uid": args.DatasourceUID},
		Queries:    queries,
		Range:      args.TimeRange,
	}
	panesJSON, err := json.Marshal(map[string]explorePane{"0": pane})
	if err != nil {
		return "", fmt.Errorf("marshal panes: %w", err)
	}
	params := url.Values{}
	params.Set("panes", string(panesJSON))
	params.Set("schemaVersion", "1")
	if orgID <= 0 {
		orgID = 1
	}
	params.Set("orgId", strconv.FormatInt(orgID, 10))
	theme := args.Theme
	if theme == "" {
		theme = "dark"
	}
	params.Set("theme", theme)
	for key, value := range args.Variables {
		params.Set(key, value)
	}
	result := strings.TrimRight(baseURL, "/") + "/explore?" + params.Encode()
	if len(result) > maxExploreURLLength {
		return "", fmt.Errorf("Explore URL exceeds %d bytes", maxExploreURLLength)
	}
	return result, nil
}

type exploreReadyState struct {
	Ready bool   `json:"ready"`
	Error string `json:"error"`
}

func waitForExploreReady() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		const script = `(() => {
			const visible = (element) => {
				if (!element) return false;
				const rect = element.getBoundingClientRect();
				return rect.width > 0 && rect.height > 0 &&
					getComputedStyle(element).visibility !== "hidden";
			};
			const errors = [
				"[data-testid*='error']",
				".alert-error",
				".query-editor-row .alert"
			];
			for (const selector of errors) {
				const element = document.querySelector(selector);
				if (visible(element) && element.textContent.trim()) {
					return {ready: false, error: element.textContent.trim().slice(0, 500)};
				}
			}
			const loading = [
				".panel-loading",
				"[aria-label='Loading']",
				"[data-testid*='loading']"
			].some((selector) => visible(document.querySelector(selector)));
			const visualizations = [
				".explore-container .panel-content",
				".explore-container [data-testid*='panel content']",
				".explore-container canvas",
				".explore-container svg"
			];
			const ready = !loading && visualizations.some((selector) => visible(document.querySelector(selector)));
			return {ready, error: ""};
		})()`
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			var state exploreReadyState
			if err := chromedp.Evaluate(script, &state).Do(ctx); err != nil {
				return err
			}
			if state.Error != "" {
				return fmt.Errorf("Grafana Explore query error: %s", state.Error)
			}
			if state.Ready {
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("timeout waiting for Explore visualization: %w", ctx.Err())
			case <-ticker.C:
			}
		}
	})
}

type exploreClip struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func captureExploreScreenshot(crop string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var clip exploreClip
		if crop == "exploreVisualization" {
			const script = `(() => {
				const selectors = [
					".explore-container .panel-content",
					".explore-container [data-testid*='panel content']",
					".explore-container canvas",
					".explore-container svg"
				];
				for (const selector of selectors) {
					const element = document.querySelector(selector);
					if (!element) continue;
					const rect = element.getBoundingClientRect();
					if (rect.width > 0 && rect.height > 0) {
						return {x: rect.x, y: rect.y, width: rect.width, height: rect.height};
					}
				}
				return {x: 0, y: 0, width: 0, height: 0};
			})()`
			if err := chromedp.Evaluate(script, &clip).Do(ctx); err != nil {
				return err
			}
			if clip.Width <= 0 || clip.Height <= 0 {
				return fmt.Errorf("Explore visualization element was not found")
			}
		}
		var image []byte
		capture := page.CaptureScreenshot().
			WithFormat(page.CaptureScreenshotFormatPng).
			WithCaptureBeyondViewport(false)
		if crop == "exploreVisualization" {
			capture = capture.WithClip(&page.Viewport{
				X: clip.X, Y: clip.Y, Width: clip.Width, Height: clip.Height, Scale: 1,
			})
		}
		var err error
		image, err = capture.Do(ctx)
		if err != nil {
			return err
		}
		return storeScreenshot(ctx, image)
	})
}

func validateArtifactPath(root, outputPath string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("artifact output root is not configured; set --artifact-output-root or GRAFANA_ARTIFACT_OUTPUT_ROOT")
	}
	if !filepath.IsAbs(outputPath) {
		return "", fmt.Errorf("outputPath must be absolute")
	}
	if strings.ToLower(filepath.Ext(outputPath)) != ".png" {
		return "", fmt.Errorf("outputPath must have a .png extension")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve artifact root: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve artifact root: %w", err)
	}
	if info, err := os.Stat(rootReal); err != nil || !info.IsDir() {
		if err != nil {
			return "", fmt.Errorf("artifact root is not accessible: %w", err)
		}
		return "", fmt.Errorf("artifact root is not a directory")
	}
	outputAbs := filepath.Clean(outputPath)
	rel, err := filepath.Rel(rootAbs, outputAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("outputPath must be below the configured artifact output root")
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(outputAbs))
	if err != nil {
		return "", fmt.Errorf("output directory must already exist: %w", err)
	}
	realRel, err := filepath.Rel(rootReal, parentReal)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("outputPath resolves outside the configured artifact output root")
	}
	return outputAbs, nil
}

func writeArtifactAtomically(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".mcp-grafana-render-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

var RenderExploreImage = mcpgrafana.MustTool(
	"render_explore_image",
	"Render a Grafana Explore query as a PNG using authenticated local headless Chrome. Writes metadata and the artifact path instead of returning image bytes. Requires --artifact-output-root or GRAFANA_ARTIFACT_OUTPUT_ROOT.",
	renderExploreImage,
	mcp.WithTitleAnnotation("Render Explore image (local browser)"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
