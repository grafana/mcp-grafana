package mcpgrafana

import (
	"context"
	"fmt"
	"sync"

	"github.com/grafana/incident-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RuntimeGrafanaURL stores the connection selected by set_grafana_url for one
// stdio server process. Stdio has one MCP client for the process lifetime, so
// this is session-scoped in practice. HTTP transports use their existing
// request-scoped X-Grafana-URL configuration instead.
type RuntimeGrafanaURL struct {
	mu                      sync.RWMutex
	url                     string
	apiKey                  string
	configured              bool
	startupTargetConfigured bool
}

const grafanaTargetNotConfiguredMessage = "No Grafana target is selected. Call `set_grafana_url` with the store name or Grafana URL before using this tool."

var runtimeGrafanaURLExemptTools = map[string]struct{}{
	"set_grafana_url":                 {},
	"search_docs":                     {},
	"get_doc":                         {},
	"suggest_loki_alloy_label_config": {},
	"get_query_examples":              {},
}

// NewRuntimeGrafanaURL creates a runtime connection store. A true argument
// declares that GRAFANA_URL supplied the startup target, so callers can use
// Grafana tools before selecting a replacement runtime target.
func NewRuntimeGrafanaURL(startupTargetConfigured ...bool) *RuntimeGrafanaURL {
	runtimeURL := &RuntimeGrafanaURL{}
	if len(startupTargetConfigured) > 0 {
		runtimeURL.startupTargetConfigured = startupTargetConfigured[0]
	}
	return runtimeURL
}

// Set replaces the runtime-selected Grafana URL and service-account token.
func (r *RuntimeGrafanaURL) Set(url, apiKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.url = url
	r.apiKey = apiKey
	r.configured = true
}

func (r *RuntimeGrafanaURL) get() (url, apiKey string, ok bool) {
	if r == nil {
		return "", "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.url, r.apiKey, r.configured
}

func (r *RuntimeGrafanaURL) requiresTargetSelection() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.configured && !r.startupTargetConfigured
}

// RuntimeGrafanaURLMiddleware rebuilds request-scoped clients from the most
// recently selected stdio connection before every MCP request. The set tool
// changes the store; its effects begin with the next request.
func RuntimeGrafanaURLMiddleware(runtimeURL *RuntimeGrafanaURL) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			url, apiKey, ok := runtimeURL.get()
			if !ok {
				if runtimeURL.requiresTargetSelection() && shouldRequireGrafanaTarget(method, req) {
					return NewToolResultError(grafanaTargetNotConfiguredMessage), nil
				}
				return next(ctx, method, req)
			}

			config := GrafanaConfigFromContext(ctx)
			config.URL = url
			config.APIKey = apiKey
			// The selected target must not inherit credentials or arbitrary
			// headers configured for the process's original target.
			config.BasicAuth = nil
			config.AccessToken = ""
			config.IDToken = ""
			config.ExtraHeaders = nil
			config.OverrideURL = url
			ctx = WithGrafanaConfig(ctx, config)
			ctx = WithGrafanaClient(ctx, NewGrafanaClient(ctx, config.URL, config.APIKey, nil))
			ctx = withRuntimeKubernetesClient(ctx)
			ctx = WithIncidentClient(ctx, newIncidentClient(ctx))

			return next(ctx, method, req)
		}
	}
}

func shouldRequireGrafanaTarget(method string, req mcp.Request) bool {
	if method != "tools/call" {
		return false
	}
	call, ok := req.(*mcp.CallToolRequest)
	if !ok || call.Params == nil {
		return false
	}
	_, exempt := runtimeGrafanaURLExemptTools[call.Params.Name]
	return !exempt
}

func withRuntimeKubernetesClient(ctx context.Context) context.Context {
	client, err := NewKubernetesClient(ctx)
	if err != nil {
		LoggerFromContext(ctx).Warn("Failed to create Kubernetes client for runtime Grafana URL; k8s APIs will be unavailable", "error", err)
		return WithKubernetesClient(ctx, nil)
	}
	return WithKubernetesClient(ctx, client)
}

func newIncidentClient(ctx context.Context) *incident.Client {
	config := GrafanaConfigFromContext(ctx)
	incidentURL := fmt.Sprintf("%s/api/plugins/grafana-irm-app/resources/api/v1/", config.URL)
	client := incident.NewClient(incidentURL, config.APIKey)

	if transport, ok := config.clientTransport(nil, WithoutAuth()); ok {
		client.HTTPClient.Transport = transport
	} else if config.OverrideURL != "" {
		client.HTTPClient.Transport = failClosedTransport(fmt.Errorf("grafana URL override transport could not be built"))
	}
	rejectIncidentErrorStatuses(client)
	return client
}
