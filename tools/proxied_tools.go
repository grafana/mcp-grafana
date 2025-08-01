// Package tools provides tools for the Grafana MCP server.
//
// Proxied tools usage:
//   - MCP server (reads env vars): AddProxiedTools(mcp)
//   - Plugin/library usage: AddProxiedToolsWithConfig(mcp, config)
package tools

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ProxyConfig holds configuration for proxy handlers
type ProxyConfig struct {
	// Tempo-specific configuration
	TempoEnabled       bool
	TempoPollingInterval time.Duration
}

// ProxiedToolsConfig holds the complete configuration for proxied tools
type ProxiedToolsConfig struct {
	// Grafana connection settings
	GrafanaURL    string
	GrafanaAPIKey string
	
	// Proxy-specific settings
	ProxyConfig ProxyConfig
}

// DefaultProxiedToolsConfig returns the default configuration for proxied tools
func DefaultProxiedToolsConfig() ProxiedToolsConfig {
	return ProxiedToolsConfig{
		ProxyConfig: ProxyConfig{
			TempoEnabled:         true,
			TempoPollingInterval: 5 * time.Minute,
		},
	}
}

// proxyConfigKey is the context key for proxy configuration
type proxyConfigKey struct{}

// WithProxyConfig sets the proxy configuration in the context
func WithProxyConfig(ctx context.Context, config ProxyConfig) context.Context {
	return context.WithValue(ctx, proxyConfigKey{}, config)
}

// ProxyConfigFromContext retrieves the proxy configuration from the context
func ProxyConfigFromContext(ctx context.Context) ProxyConfig {
	config, ok := ctx.Value(proxyConfigKey{}).(ProxyConfig)
	if !ok {
		// Return default configuration
		return ProxyConfig{
			TempoEnabled:         os.Getenv("TEMPO_PROXY_ENABLED") != "false",
			TempoPollingInterval: 5 * time.Minute,
		}
	}
	return config
}

// ProxyHandler defines the interface for datasource-specific proxy implementations
type ProxyHandler interface {
	// Initialize discovers and registers tools for this datasource type
	Initialize(ctx context.Context, mcp *server.MCPServer)
	// Shutdown cleans up resources for this datasource type
	Shutdown()
}

// Registry of proxy handlers by datasource type
var (
	proxyHandlers = make(map[string]ProxyHandler)
	handlersMutex sync.RWMutex
)

// RegisterProxyHandler registers a handler for a specific datasource type
func RegisterProxyHandler(datasourceType string, handler ProxyHandler) {
	handlersMutex.Lock()
	defer handlersMutex.Unlock()
	proxyHandlers[datasourceType] = handler
}

// AddProxiedTools initializes all registered proxy handlers using environment variables
// This function maintains backward compatibility for the standalone MCP server use case
func AddProxiedTools(mcp *server.MCPServer) {
	// Read configuration from environment variables
	config := ProxiedToolsConfig{
		GrafanaURL:    os.Getenv("GRAFANA_URL"),
		GrafanaAPIKey: os.Getenv("GRAFANA_API_KEY"),
		ProxyConfig: ProxyConfig{
			TempoEnabled:         os.Getenv("TEMPO_PROXY_ENABLED") != "false",
			TempoPollingInterval: parsePollingInterval(os.Getenv("TEMPO_POLLING_INTERVAL")),
		},
	}
	
	AddProxiedToolsWithConfig(mcp, config)
}

// AddProxiedToolsWithConfig initializes all registered proxy handlers with the provided configuration
// This function can be used by plugins or other integrations that need to provide configuration programmatically
//
// Example usage in a Grafana plugin:
//
//	config := tools.DefaultProxiedToolsConfig()
//	config.GrafanaURL = "https://grafana.example.com"
//	config.GrafanaAPIKey = "my-api-key"
//	config.ProxyConfig.TempoEnabled = true
//	tools.AddProxiedToolsWithConfig(mcpServer, config)
func AddProxiedToolsWithConfig(mcp *server.MCPServer, config ProxiedToolsConfig) {
	AddProxiedToolsWithContext(context.Background(), mcp, config)
}

// AddProxiedToolsWithContext initializes all registered proxy handlers with the provided context and configuration
// This is the most flexible initialization function, allowing full control over both context and configuration
func AddProxiedToolsWithContext(ctx context.Context, mcp *server.MCPServer, config ProxiedToolsConfig) {
	handlersMutex.RLock()
	defer handlersMutex.RUnlock()
	
	// Add proxy configuration to the context
	ctx = WithProxyConfig(ctx, config.ProxyConfig)
	
	// Set up Grafana configuration if provided
	if config.GrafanaURL != "" {
		gc := mcpgrafana.GrafanaConfig{
			URL:    config.GrafanaURL,
			APIKey: config.GrafanaAPIKey,
		}
		ctx = mcpgrafana.WithGrafanaConfig(ctx, gc)
		
		// Create Grafana client
		client := mcpgrafana.NewGrafanaClient(ctx, config.GrafanaURL, config.GrafanaAPIKey)
		ctx = mcpgrafana.WithGrafanaClient(ctx, client)
	}
	
	for dsType, handler := range proxyHandlers {
		slog.Info("Initializing proxy handler", "datasource_type", dsType)
		handler.Initialize(ctx, mcp)
	}
}

// parsePollingInterval parses a duration string with a default fallback
func parsePollingInterval(intervalStr string) time.Duration {
	if intervalStr == "" {
		return 5 * time.Minute
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		slog.Warn("Invalid polling interval, using default", "value", intervalStr, "error", err)
		return 5 * time.Minute
	}
	return interval
}

// StopProxiedTools shuts down all registered proxy handlers
func StopProxiedTools() {
	handlersMutex.RLock()
	defer handlersMutex.RUnlock()
	
	for dsType, handler := range proxyHandlers {
		slog.Info("Shutting down proxy handler", "datasource_type", dsType)
		handler.Shutdown()
	}
}

// ProxyDatasource holds information about a datasource that supports MCP proxy
type ProxyDatasource struct {
	ID   int64
	UID  string
	Name string
	URL  string
	Type string
}

// JSON-RPC structures for MCP communication
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

// ProxySession represents a session with a specific datasource
type ProxySession struct {
	ID           string
	DatasourceID int64
	Tools        []mcp.Tool
	Initialized  bool
	LastUsed     time.Time
}

// SessionManager manages sessions for multiple datasources
type SessionManager struct {
	sessions map[string]*ProxySession // Maps datasource UID to session
	mu       sync.RWMutex
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*ProxySession),
	}
}

// GetSession retrieves or creates a session for the given datasource
func (sm *SessionManager) GetSession(datasourceUID string, datasourceID int64) *ProxySession {
	sm.mu.RLock()
	session, exists := sm.sessions[datasourceUID]
	sm.mu.RUnlock()
	
	if exists {
		// Update last used time
		sm.mu.Lock()
		session.LastUsed = time.Now()
		sm.mu.Unlock()
		return session
	}
	
	// Create new session
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	// Double-check in case another goroutine created it
	if session, exists = sm.sessions[datasourceUID]; exists {
		session.LastUsed = time.Now()
		return session
	}
	
	session = &ProxySession{
		DatasourceID: datasourceID,
		Initialized:  false,
		LastUsed:     time.Now(),
	}
	sm.sessions[datasourceUID] = session
	
	return session
}

// SetSessionID updates the session ID for a datasource
func (sm *SessionManager) SetSessionID(datasourceUID string, sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	if session, exists := sm.sessions[datasourceUID]; exists {
		session.ID = sessionID
	}
}

// SetTools updates the tools for a datasource session
func (sm *SessionManager) SetTools(datasourceUID string, tools []mcp.Tool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	if session, exists := sm.sessions[datasourceUID]; exists {
		session.Tools = tools
		session.Initialized = true
	}
}

// CleanupStaleSessions removes sessions that haven't been used in the specified duration
func (sm *SessionManager) CleanupStaleSessions(maxAge time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	now := time.Now()
	for uid, session := range sm.sessions {
		if now.Sub(session.LastUsed) > maxAge {
			delete(sm.sessions, uid)
		}
	}
}

// Global variables for session and datasource management
var (
	proxyDatasources     map[string]map[string]ProxyDatasource // Maps type -> UID -> datasource info
	datasourcesLock      sync.RWMutex
	sessionManager       = NewSessionManager()
	jsonrpcRequestIDCounter int64 // Atomic counter for JSON-RPC request IDs
	discoveryStopChan    chan struct{} // Channel to stop the discovery goroutine
	discoveryRunning     bool
	discoveryMutex       sync.Mutex
)

// Initialize package-level variables
func init() {
	proxyDatasources = make(map[string]map[string]ProxyDatasource)
}

// startPeriodicDiscovery starts a background goroutine that periodically discovers datasources
func startPeriodicDiscovery(ctx context.Context, interval time.Duration) {
	discoveryMutex.Lock()
	if discoveryRunning {
		discoveryMutex.Unlock()
		return
	}
	discoveryRunning = true
	discoveryStopChan = make(chan struct{})
	discoveryMutex.Unlock()
	
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				// Clean up stale sessions (older than 1 hour)
				sessionManager.CleanupStaleSessions(time.Hour)
				
			case <-discoveryStopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// stopPeriodicDiscovery stops the background discovery goroutine
func stopPeriodicDiscovery() {
	discoveryMutex.Lock()
	defer discoveryMutex.Unlock()
	
	if discoveryRunning && discoveryStopChan != nil {
		close(discoveryStopChan)
		discoveryRunning = false
	}
}

// getNextRequestID returns the next JSON-RPC request ID
func getNextRequestID() int64 {
	return atomic.AddInt64(&jsonrpcRequestIDCounter, 1)
}

// discoverDatasources queries Grafana API to find datasources of a specific type
func discoverDatasources(ctx context.Context, datasourceType string) (map[string]ProxyDatasource, error) {
	// Get the Grafana client from context
	client := mcpgrafana.GrafanaClientFromContext(ctx)
	if client == nil {
		return nil, fmt.Errorf("grafana client not found in context")
	}
	
	// List all datasources
	resp, err := client.Datasources.GetDataSources()
	if err != nil {
		return nil, fmt.Errorf("failed to list datasources: %w", err)
	}
	
	// Filter for datasources of the specified type and build map
	datasources := make(map[string]ProxyDatasource)
	for _, ds := range resp.Payload {
		if strings.EqualFold(ds.Type, datasourceType) {
			datasources[ds.UID] = ProxyDatasource{
				ID:   ds.ID,
				UID:  ds.UID,
				Name: ds.Name,
				URL:  ds.URL,
				Type: ds.Type,
			}
		}
	}
	
	if len(datasources) == 0 {
		return nil, fmt.Errorf("no %s datasources found", datasourceType)
	}
	
	return datasources, nil
}

// getDatasource retrieves a datasource by UID and type
func getDatasource(ctx context.Context, datasourceType, uid string) (*ProxyDatasource, error) {
	datasourcesLock.RLock()
	typeDatasources, typeExists := proxyDatasources[datasourceType]
	if typeExists {
		ds, exists := typeDatasources[uid]
		datasourcesLock.RUnlock()
		if exists {
			return &ds, nil
		}
	} else {
		datasourcesLock.RUnlock()
	}
	
	// Datasource not in cache, try to discover
	discovered, err := discoverDatasources(ctx, datasourceType)
	if err != nil {
		return nil, fmt.Errorf("failed to discover %s datasources: %w", datasourceType, err)
	}
	
	// Update cache
	datasourcesLock.Lock()
	if proxyDatasources[datasourceType] == nil {
		proxyDatasources[datasourceType] = make(map[string]ProxyDatasource)
	}
	proxyDatasources[datasourceType] = discovered
	datasourcesLock.Unlock()
	
	// Check again
	ds, exists := discovered[uid]
	if !exists {
		return nil, fmt.Errorf("%s datasource with UID '%s' not found", datasourceType, uid)
	}
	
	return &ds, nil
}

// callMCP makes a JSON-RPC call to an MCP server through Grafana proxy
func callMCP(ctx context.Context, datasourceUID string, method string, params interface{}) (*JSONRPCResponse, error) {
	// Extract Grafana configuration from context
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL not found in context")
	}
	
	// Get the datasource information - we need to determine the type from the UID
	// For now, we'll check all known types
	var ds *ProxyDatasource
	var err error
	
	// Try each known type
	for dsType := range proxyDatasources {
		ds, err = getDatasource(ctx, dsType, datasourceUID)
		if err == nil {
			break
		}
	}
	
	if ds == nil {
		// Try to discover from Tempo (default for now)
		ds, err = getDatasource(ctx, "tempo", datasourceUID)
		if err != nil {
			return nil, fmt.Errorf("failed to get datasource: %w", err)
		}
	}
	
	// Get or create session for this datasource
	session := sessionManager.GetSession(datasourceUID, ds.ID)
	
	// Construct proxy URL using the datasource ID
	proxyURL := fmt.Sprintf("%s/api/datasources/proxy/%d/api/mcp", strings.TrimRight(cfg.URL, "/"), ds.ID)
	
	request := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      int(getNextRequestID()),
		Method:  method,
		Params:  params,
	}
	
	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	req, err := http.NewRequest("POST", proxyURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	
	// Add session ID header if we have one
	if session.ID != "" {
		req.Header.Set("Mcp-Session-Id", session.ID)
	}
	
	// Add authentication based on configuration
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	}
	
	// Create HTTP client with TLS configuration if available
	client := &http.Client{
		Timeout: 30 * time.Second, // Add timeout to prevent hanging
	}
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: tlsConfig.SkipVerify,
			},
		}
		
		// Create proper TLS config if certificates are provided
		if tlsConfig.CertFile != "" || tlsConfig.KeyFile != "" || tlsConfig.CAFile != "" {
			tlsCfg, err := tlsConfig.CreateTLSConfig()
			if err != nil {
				return nil, fmt.Errorf("failed to create TLS config: %w", err)
			}
			transport.TLSClientConfig = tlsCfg
		}
		
		client.Transport = transport
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	
	// Check if this is a text error response instead of JSON
	bodyStr := string(body)
	if strings.HasPrefix(bodyStr, "Invalid session ID") || strings.HasPrefix(bodyStr, "No session") {
		// Session expired, clear it and retry
		sessionManager.SetSessionID(datasourceUID, "")
		session.Initialized = false
		return nil, fmt.Errorf("session expired, please retry: %s", bodyStr)
	}
	
	var jsonResp JSONRPCResponse
	if err := json.Unmarshal(body, &jsonResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response (body: %s): %w", bodyStr, err)
	}
	
	if jsonResp.Error != nil {
		return nil, fmt.Errorf("MCP error: %v", jsonResp.Error)
	}
	
	// Extract session ID from Set-Cookie or response headers
	if method == "initialize" {
		// Check Mcp-Session-Id header (used by Tempo)
		if sessionHeader := resp.Header.Get("Mcp-Session-Id"); sessionHeader != "" {
			sessionManager.SetSessionID(datasourceUID, sessionHeader)
		} else if sessionCookie := resp.Header.Get("Set-Cookie"); sessionCookie != "" {
			// Parse session ID from cookie
			if strings.Contains(sessionCookie, "session_id=") {
				parts := strings.Split(sessionCookie, "session_id=")
				if len(parts) > 1 {
					sessionPart := strings.Split(parts[1], ";")[0]
					sessionManager.SetSessionID(datasourceUID, sessionPart)
				}
			}
		} else if sessionHeader := resp.Header.Get("X-Session-ID"); sessionHeader != "" {
			// Also check X-Session-ID header
			sessionManager.SetSessionID(datasourceUID, sessionHeader)
		}
	}
	
	return &jsonResp, nil
}

// ensureSession initializes the MCP session if not already done
func ensureSession(ctx context.Context, datasourceUID string) error {
	// Get the datasource information - we need to determine the type
	var ds *ProxyDatasource
	var err error
	
	// Try each known type
	for dsType := range proxyDatasources {
		ds, err = getDatasource(ctx, dsType, datasourceUID)
		if err == nil {
			break
		}
	}
	
	if ds == nil {
		// Try Tempo as default
		ds, err = getDatasource(ctx, "tempo", datasourceUID)
		if err != nil {
			return fmt.Errorf("failed to get datasource: %w", err)
		}
	}
	
	session := sessionManager.GetSession(datasourceUID, ds.ID)
	
	if session.Initialized {
		return nil
	}
	
	// Initialize the session with retry logic
	const maxRetries = 3
	var lastErr error
	
	for retry := 0; retry < maxRetries; retry++ {
		if retry > 0 {
			// Exponential backoff
			time.Sleep(time.Duration(retry*retry) * time.Second)
		}
		
		// Initialize the session
		initParams := mcp.InitializeParams{
			ProtocolVersion: "2024-11-05",
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "grafana-mcp-server",
				Version: "1.0",
			},
		}
		
		_, err = callMCP(ctx, datasourceUID, "initialize", initParams)
		if err != nil {
			lastErr = err
			// Check if it's a session error, if so, retry
			if strings.Contains(err.Error(), "session expired") {
				continue
			}
			// For other errors, don't retry
			return fmt.Errorf("failed to initialize session: %w", err)
		}
		
		// List tools
		resp, err := callMCP(ctx, datasourceUID, "tools/list", nil)
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "session expired") {
				continue
			}
			return fmt.Errorf("failed to list tools: %w", err)
		}
		
		// Parse tools response
		resultBytes, err := json.Marshal(resp.Result)
		if err != nil {
			return fmt.Errorf("failed to marshal tools result: %w", err)
		}
		
		var toolsResult mcp.ListToolsResult
		if err := json.Unmarshal(resultBytes, &toolsResult); err != nil {
			return fmt.Errorf("failed to unmarshal tools result: %w", err)
		}
		
		sessionManager.SetTools(datasourceUID, toolsResult.Tools)
		
		// Success!
		return nil
	}
	
	return fmt.Errorf("failed to initialize session after %d retries: %w", maxRetries, lastErr)
}

// StartProxyDiscovery starts the periodic discovery of datasources
// This should be called after the server has been initialized with Grafana configuration
func StartProxyDiscovery(ctx context.Context, interval time.Duration) {
	// Start periodic discovery with default interval of 5 minutes if not specified
	if interval == 0 {
		interval = 5 * time.Minute
	}
	
	startPeriodicDiscovery(ctx, interval)
}

// StopProxyDiscovery stops the periodic discovery of datasources
func StopProxyDiscovery() {
	stopPeriodicDiscovery()
} 
