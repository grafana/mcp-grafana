package mcpgrafana

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	mcp_client "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	// mcpClientInitTimeout is the timeout for initializing a proxied MCP client
	// (connecting, handshaking, and listing tools). Kept short to avoid blocking
	// server startup when a datasource's MCP endpoint is slow or unreachable.
	mcpClientInitTimeout = 30 * time.Second

	// mcpRetryMaxAttempts is the total number of attempts (including the first)
	// made for a probe or connect operation before giving up on a candidate.
	mcpRetryMaxAttempts = 2
	// mcpRetryBackoff is the base backoff between retry attempts; attempt N
	// waits N*mcpRetryBackoff before retrying.
	mcpRetryBackoff = 250 * time.Millisecond
)

// retryPolicy configures withRetry.
type retryPolicy struct {
	maxAttempts int
	backoff     time.Duration
}

// defaultRetryPolicy is used for both MCP-support probes and client connects.
var defaultRetryPolicy = retryPolicy{maxAttempts: mcpRetryMaxAttempts, backoff: mcpRetryBackoff}

// transientError marks a failure as retryable (a timeout, network/dial error,
// or server error that may well succeed on a subsequent attempt). Any error
// NOT wrapped as a transientError is treated as deterministic by withRetry and
// is never retried.
type transientError struct{ err error }

// newTransientError wraps err as transient. Returns nil if err is nil, so it
// can be used directly as a return value: `return result, newTransientError(err)`.
func newTransientError(err error) error {
	if err == nil {
		return nil
	}
	return &transientError{err: err}
}

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// isTransient reports whether err (or anything it wraps) is a transientError.
func isTransient(err error) bool {
	var te *transientError
	return errors.As(err, &te)
}

// withRetry runs attempt up to policy.maxAttempts times. attempt receives the
// 1-based attempt number, so callers can distinguish first tries from retries
// (e.g. for metrics) or build a fresh per-attempt timeout context.
//
//   - attempt returns (result, nil): success, returned immediately.
//   - attempt returns a transientError (see newTransientError): retried after
//     waiting attemptNum*policy.backoff, unless attempts are exhausted or ctx
//     is done first.
//   - attempt returns any other non-nil error: deterministic, returned
//     immediately with no retry.
//
// On exhaustion, the returned error wraps the last attempt's error with the
// attempt count, and still satisfies isTransient, so a caller's final log
// message is self-describing (e.g. "... failed after 2 attempts: <err>").
func withRetry[T any](ctx context.Context, policy retryPolicy, description string, attempt func(attemptNum int) (T, error)) (T, error) {
	var zero T
	var lastErr error
	for n := 1; n <= policy.maxAttempts; n++ {
		result, err := attempt(n)
		if err == nil {
			return result, nil
		}
		if !isTransient(err) {
			return zero, err
		}
		lastErr = err
		if n == policy.maxAttempts {
			break
		}

		select {
		case <-time.After(time.Duration(n) * policy.backoff):
		case <-ctx.Done():
			return zero, newTransientError(contextCauseOrErr(ctx, ctx.Err()))
		}
	}
	return zero, newTransientError(fmt.Errorf("%s failed after %d attempts: %w", description, policy.maxAttempts, lastErr))
}

// classifyConnectError decides whether a NewProxiedClient failure is worth
// retrying. Auth rejections are deterministic: retrying with the same
// credentials cannot help. Everything else (handshake timeout, dial/network
// error, or any other Initialize/ListTools failure) is treated as transient.
//
// This deliberately over-retries a few genuinely deterministic failures we
// can't cheaply classify (e.g. a persistently-misconfigured endpoint), at the
// cost of one extra mcpClientInitTimeout-bounded attempt per candidate. That
// trade-off favors fixing the under-retry of transient failures that caused
// production flapping over precisely classifying every failure mode.
func classifyConnectError(err error) error {
	if err == nil {
		return nil
	}
	var authErr *transport.AuthorizationRequiredError
	var oauthErr *transport.OAuthAuthorizationRequiredError
	if errors.As(err, &authErr) || errors.As(err, &oauthErr) {
		return err
	}
	return newTransientError(err)
}

// ProxiedClient represents a connection to a remote MCP server (e.g., Tempo
// datasource). It caches the upstream server's tools, resources, resource
// templates, and prompts so they can be re-advertised by the parent server
// without re-querying the upstream.
type ProxiedClient struct {
	DatasourceUID     string
	DatasourceName    string
	DatasourceType    string
	Client            *mcp_client.Client
	Tools             []mcp.Tool
	Resources         []mcp.Resource
	ResourceTemplates []mcp.ResourceTemplate
	Prompts           []mcp.Prompt
	mutex             sync.RWMutex

	// closeHook, when set, runs inside Close (under the client mutex). It is a
	// test seam so lifecycle tests can observe exactly how many times a client is
	// Closed without standing up a real remote MCP transport. Always nil in
	// production; do not use it for behavior.
	closeHook func()
}

// contextCauseOrErr returns the context cause if the error is due to context
// cancellation, otherwise returns the original error.
func contextCauseOrErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
	}
	return err
}

// proxiedClientOption configures NewProxiedClient.
type proxiedClientOption func(*proxiedClientOptions)

type proxiedClientOptions struct {
	metrics *discoveryMetrics
}

// withProxiedClientMetrics lets the caller (the ToolManager, which owns the
// injected MeterProvider) hand its instruments to NewProxiedClient so the
// upstream capability inventory taken at connect time is recorded. Optional:
// call sites without a MeterProvider (tests) omit it and record nothing.
func withProxiedClientMetrics(m discoveryMetrics) proxiedClientOption {
	return func(o *proxiedClientOptions) {
		o.metrics = &m
	}
}

// NewProxiedClient creates a new connection to a remote MCP server
func NewProxiedClient(ctx context.Context, datasourceUID, datasourceName, datasourceType, mcpEndpoint string, opts ...proxiedClientOption) (*ProxiedClient, error) {
	var options proxiedClientOptions
	for _, opt := range opts {
		opt(&options)
	}

	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()

	initCtx, cancel := context.WithTimeoutCause(ctx, mcpClientInitTimeout,
		fmt.Errorf("timed out after %s connecting to MCP server for datasource %s (%s) at %s", mcpClientInitTimeout, datasourceName, datasourceUID, mcpEndpoint))
	defer cancel()

	rt, err := BuildTransport(&config, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport: %w", err)
	}

	logger.DebugContext(initCtx, "connecting to MCP server", "datasource", datasourceUID, "url", mcpEndpoint)
	httpTransport, err := transport.NewStreamableHTTP(
		mcpEndpoint,
		transport.WithHTTPBasicClient(&http.Client{Transport: rt}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP transport: %w", err)
	}

	// Create MCP client
	mcpClient := mcp_client.NewClient(httpTransport)

	// Initialize the connection
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-grafana-proxy",
		Version: Version(),
	}

	_, err = mcpClient.Initialize(initCtx, initReq)
	if err != nil {
		_ = mcpClient.Close()
		return nil, fmt.Errorf("failed to initialize MCP client: %w", contextCauseOrErr(initCtx, err))
	}

	// List available tools from the remote server. A server that answers
	// METHOD_NOT_FOUND has no tools but may still expose resources or prompts,
	// so that case is tolerated rather than failing the connection; any other
	// error is a real connect failure.
	var tools []mcp.Tool
	toolsResult, err := mcpClient.ListTools(initCtx, mcp.ListToolsRequest{})
	switch {
	case err == nil:
		tools = toolsResult.Tools
	case errors.Is(err, mcp.ErrMethodNotFound):
		logger.DebugContext(initCtx, "remote MCP server does not support tools/list",
			"datasource", datasourceUID)
	default:
		_ = mcpClient.Close()
		return nil, fmt.Errorf("failed to list tools from remote MCP server: %w", contextCauseOrErr(initCtx, err))
	}

	caps := mcpClient.GetServerCapabilities()

	// Fetch resources and resource templates. Many servers (Tempo included)
	// register resources without declaring the capability, so these are probed
	// even when caps.Resources is nil, and every failure is non-fatal.
	inventory := remoteInventory{caps: caps, uid: datasourceUID, dsType: datasourceType, metrics: options.metrics}
	resources, resourceTemplates := inventory.listResources(initCtx, mcpClient, logger)
	prompts := inventory.listPrompts(initCtx, mcpClient, logger)

	logger.DebugContext(initCtx, "connected to proxied MCP server",
		"datasource", datasourceUID,
		"type", datasourceType,
		"tools", len(tools),
		"resources", len(resources),
		"resource_templates", len(resourceTemplates),
		"prompts", len(prompts))

	return &ProxiedClient{
		DatasourceUID:     datasourceUID,
		DatasourceName:    datasourceName,
		DatasourceType:    datasourceType,
		Client:            mcpClient,
		Tools:             tools,
		Resources:         resources,
		ResourceTemplates: resourceTemplates,
		Prompts:           prompts,
	}, nil
}

// remoteInventory gathers the non-tool capabilities of one upstream MCP server.
// Unlike tools/list, none of these lists are required for a usable connection:
// every failure is logged (and counted, when metrics were supplied) and yields
// an empty result, so a partially-supporting upstream still proxies whatever it
// does expose.
type remoteInventory struct {
	caps    mcp.ServerCapabilities
	uid     string
	dsType  string
	metrics *discoveryMetrics
}

// recordListFailure counts an upstream capability listing that failed for a
// reason other than METHOD_NOT_FOUND (which is a supported answer, not a
// failure).
func (ri remoteInventory) recordListFailure(ctx context.Context, capability string) {
	if ri.metrics == nil || ri.metrics.upstreamListFailure == nil {
		return
	}
	ri.metrics.upstreamListFailure.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrDatasourceType, ri.dsType),
		attribute.String(attrProxiedCapability, capability),
	))
}

// listResources fetches the upstream's static resources and resource templates.
func (ri remoteInventory) listResources(ctx context.Context, c *mcp_client.Client, logger *slog.Logger) ([]mcp.Resource, []mcp.ResourceTemplate) {
	var resources []mcp.Resource
	resourcesResult, err := c.ListResources(ctx, mcp.ListResourcesRequest{})
	switch {
	case err == nil:
		resources = resourcesResult.Resources
	case errors.Is(err, mcp.ErrMethodNotFound):
		logger.DebugContext(ctx, "remote MCP server does not support resources/list",
			"datasource", ri.uid)
	default:
		ri.recordListFailure(ctx, capabilityResources)
		// A failure is only noteworthy when the capability was advertised;
		// otherwise this was a speculative probe of a server that likely has no
		// resources at all.
		if ri.caps.Resources != nil {
			logger.WarnContext(ctx, "failed to list resources from remote MCP server",
				"datasource", ri.uid, "error", err)
		} else {
			logger.DebugContext(ctx, "resources/list probe failed",
				"datasource", ri.uid, "error", err)
		}
	}

	var resourceTemplates []mcp.ResourceTemplate
	templatesResult, err := c.ListResourceTemplates(ctx, mcp.ListResourceTemplatesRequest{})
	switch {
	case err == nil:
		resourceTemplates = templatesResult.ResourceTemplates
	case errors.Is(err, mcp.ErrMethodNotFound):
		logger.DebugContext(ctx, "remote MCP server does not support resources/templates/list",
			"datasource", ri.uid)
	default:
		ri.recordListFailure(ctx, capabilityResourceTemplates)
		if ri.caps.Resources != nil {
			logger.WarnContext(ctx, "failed to list resource templates from remote MCP server",
				"datasource", ri.uid, "error", err)
		} else {
			logger.DebugContext(ctx, "resources/templates/list probe failed",
				"datasource", ri.uid, "error", err)
		}
	}

	return resources, resourceTemplates
}

// listPrompts fetches the upstream's prompts, but only when the capability is
// advertised. Prompts are not probed blindly (unlike resources): stricter
// upstreams may treat an unknown method as an error, and there is no reason to
// expect a server that declares no prompts to have any.
func (ri remoteInventory) listPrompts(ctx context.Context, c *mcp_client.Client, logger *slog.Logger) []mcp.Prompt {
	if ri.caps.Prompts == nil {
		return nil
	}

	promptsResult, err := c.ListPrompts(ctx, mcp.ListPromptsRequest{})
	switch {
	case err == nil:
		return promptsResult.Prompts
	case errors.Is(err, mcp.ErrMethodNotFound):
		logger.DebugContext(ctx, "remote MCP server advertised prompts but returned method not found",
			"datasource", ri.uid)
	default:
		ri.recordListFailure(ctx, capabilityPrompts)
		logger.WarnContext(ctx, "failed to list prompts from remote MCP server",
			"datasource", ri.uid, "error", err)
	}
	return nil
}

// CallTool forwards a tool call to the remote MCP server
func (pc *ProxiedClient) CallTool(ctx context.Context, toolName string, arguments map[string]any) (*mcp.CallToolResult, error) {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	// Validate the tool exists
	var toolExists bool
	for _, tool := range pc.Tools {
		if tool.Name == toolName {
			toolExists = true
			break
		}
	}
	if !toolExists {
		return nil, fmt.Errorf("tool %s not found in remote MCP server", toolName)
	}

	// Create the call tool request
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = arguments

	// Forward the call to the remote server
	result, err := pc.Client.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to call tool on remote MCP server: %w", err)
	}

	return result, nil
}

// ListTools returns the tools available from this remote server
// Note: This method doesn't take a context parameter as the tools are cached locally
func (pc *ProxiedClient) ListTools() []mcp.Tool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	// Return a copy to prevent external modification
	result := make([]mcp.Tool, len(pc.Tools))
	copy(result, pc.Tools)
	return result
}

// ListResources returns the static resources cached from this remote server.
func (pc *ProxiedClient) ListResources() []mcp.Resource {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	result := make([]mcp.Resource, len(pc.Resources))
	copy(result, pc.Resources)
	return result
}

// ListResourceTemplates returns the resource templates cached from this remote server.
func (pc *ProxiedClient) ListResourceTemplates() []mcp.ResourceTemplate {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	result := make([]mcp.ResourceTemplate, len(pc.ResourceTemplates))
	copy(result, pc.ResourceTemplates)
	return result
}

// ListPrompts returns the prompts cached from this remote server.
func (pc *ProxiedClient) ListPrompts() []mcp.Prompt {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	result := make([]mcp.Prompt, len(pc.Prompts))
	copy(result, pc.Prompts)
	return result
}

// ReadResource forwards a resources/read call to the remote MCP server. uri is
// the upstream's original URI, not the namespaced URN exposed to clients.
func (pc *ProxiedClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	req := mcp.ReadResourceRequest{}
	req.Params.URI = uri

	result, err := pc.Client.ReadResource(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource on remote MCP server: %w", err)
	}
	return result, nil
}

// GetPrompt forwards a prompts/get call to the remote MCP server. promptName is
// the upstream's original prompt name, not the namespaced name.
func (pc *ProxiedClient) GetPrompt(ctx context.Context, promptName string, args map[string]string) (*mcp.GetPromptResult, error) {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	req := mcp.GetPromptRequest{}
	req.Params.Name = promptName
	req.Params.Arguments = args

	result, err := pc.Client.GetPrompt(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt on remote MCP server: %w", err)
	}
	return result, nil
}

// Close closes the connection to the remote MCP server
func (pc *ProxiedClient) Close() error {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	if pc.closeHook != nil {
		pc.closeHook()
	}

	if pc.Client != nil {
		if err := pc.Client.Close(); err != nil {
			return fmt.Errorf("failed to close MCP client: %w", err)
		}
	}

	return nil
}
