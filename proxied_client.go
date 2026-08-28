package mcpgrafana

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	mcp_client "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
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

// ProxiedClient represents a connection to a remote MCP server (e.g., Tempo datasource)
type ProxiedClient struct {
	DatasourceUID  string
	DatasourceName string
	DatasourceType string
	OrgID          int64
	Client         *mcp_client.Client
	Tools          []mcp.Tool
	mutex          sync.RWMutex

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

// NewProxiedClient creates a new connection to a remote MCP server
func NewProxiedClient(ctx context.Context, orgID int64, datasourceUID, datasourceName, datasourceType, mcpEndpoint string) (*ProxiedClient, error) {
	config := GrafanaConfigFromContext(ctx)
	logger := config.LoggerOrDefault()

	// Bake the org into this client's transport so every request to the datasource
	// proxy carries X-Grafana-Org-Id: orgID, independent of the per-call context.
	config.OrgID = orgID
	ctx = WithGrafanaConfig(ctx, config)

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

	// List available tools from the remote server
	listReq := mcp.ListToolsRequest{}
	toolsResult, err := mcpClient.ListTools(initCtx, listReq)
	if err != nil {
		_ = mcpClient.Close()
		return nil, fmt.Errorf("failed to list tools from remote MCP server: %w", contextCauseOrErr(initCtx, err))
	}

	logger.DebugContext(initCtx, "connected to proxied MCP server",
		"datasource", datasourceUID,
		"type", datasourceType,
		"tools", len(toolsResult.Tools))

	return &ProxiedClient{
		DatasourceUID:  datasourceUID,
		DatasourceName: datasourceName,
		DatasourceType: datasourceType,
		OrgID:          orgID,
		Client:         mcpClient,
		Tools:          toolsResult.Tools,
	}, nil
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
