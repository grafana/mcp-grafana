package mcpgrafana

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yosida95/uritemplate/v3"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
)

const (
	// mcpProbeTimeout is the timeout for probing a single datasource's MCP endpoint.
	// This is kept short to avoid slow startup when datasources are unreachable.
	mcpProbeTimeout = 5 * time.Second

	// proxiedToolsMeterName is the OTel meter name for discovery/connect metrics,
	// matching the convention used by clientCacheMeterName/sessionMeterName.
	proxiedToolsMeterName = "mcp-grafana"
)

// Metric attribute keys for the proxied-capabilities instrumentation. All of
// them are low cardinality: datasource.type and mcp.proxied.capability come from
// closed sets, mcp.proxied.scope is server|session, and the skip reasons are the
// literals defined below.
const (
	attrDatasourceType    = "datasource.type"
	attrProxiedCapability = "mcp.proxied.capability"
	attrProxiedScope      = "mcp.proxied.scope"
	attrProxiedSkipReason = "mcp.proxied.skip_reason"
)

// Span-only attribute keys for a single proxied request. These identify the
// datasource instance and upstream target a resources/read or prompts/get was
// routed to, which is exactly what is needed to debug a failing proxied request
// but is far too high cardinality to be a metric label (one series per
// datasource UID, and per resource URI). They mirror how mcp.tool.target is
// span-only for tool calls; see the observability package. datasource.type is
// shared with the metrics above rather than duplicated under a second name.
const (
	attrDatasourceUID         = "datasource.uid"
	attrProxiedUpstreamURI    = "mcp.proxied.upstream.uri"
	attrProxiedUpstreamPrompt = "mcp.proxied.upstream.prompt"
)

// Values for attrProxiedCapability: which part of the upstream MCP surface a
// measurement is about.
const (
	capabilityTools             = "tools"
	capabilityResources         = "resources"
	capabilityResourceTemplates = "resource_templates"
	capabilityPrompts           = "prompts"
)

// Values for attrProxiedScope: where the capability was registered. Stdio is
// single-tenant so its registration is server-wide and happens once at startup;
// HTTP/SSE registration is per session.
const (
	scopeServer  = "server"
	scopeSession = "session"
)

// Values for attrProxiedSkipReason: why an upstream capability was discovered
// but not re-exposed.
const (
	// skipReasonUnnamespaceableTemplate: the upstream's resource template could
	// not be namespaced — it had no URI template at all (which mcp-go's
	// AddResourceTemplates would dereference and panic on), or its namespaced
	// form did not parse as a URI template.
	skipReasonUnnamespaceableTemplate = "unnamespaceable_template"
	// skipReasonNoSessionPrompts: mcp-go has no AddSessionPrompts, so prompts
	// cannot be scoped to one session and are dropped on HTTP/SSE transports
	// rather than registered server-wide across tenants.
	skipReasonNoSessionPrompts = "no_session_prompt_support"
)

// discoveryMetrics holds OTel instruments for MCP datasource discovery (probe),
// connection (buildProxiedToolSet), and proxied-capability registration
// observability.
type discoveryMetrics struct {
	probeSuccess              metric.Int64Counter // Probes that found a datasource is MCP-enabled
	probeDeterministicFailure metric.Int64Counter // Probes with a clean non-retryable response (e.g. 404): not MCP-enabled
	probeTransientFailure     metric.Int64Counter // Probes that exhausted retries on a transient error (timeout/network/5xx)
	probeRetries              metric.Int64Counter // Probe attempts issued as a retry (attempts beyond the first)

	connectSuccess              metric.Int64Counter // Proxied client connections established
	connectDeterministicFailure metric.Int64Counter // Connections that failed for a non-retryable reason (e.g. auth)
	connectTransientFailure     metric.Int64Counter // Connections that exhausted retries on a transient error
	connectRetries              metric.Int64Counter // Connect attempts issued as a retry (attempts beyond the first)

	// upstreamListFailure counts non-METHOD_NOT_FOUND failures of an upstream
	// capability listing (resources/list, resources/templates/list,
	// prompts/list). These are tolerated at connect time, so without this
	// counter a datasource silently proxying no resources is indistinguishable
	// from one that genuinely has none.
	upstreamListFailure metric.Int64Counter
	// capabilitiesRegistered counts capabilities re-exposed to clients, by
	// capability and scope.
	capabilitiesRegistered metric.Int64Counter
	// capabilitiesSkipped counts capabilities discovered upstream but not
	// re-exposed, by capability and reason.
	capabilitiesSkipped metric.Int64Counter
	// registerFailure counts failures to register a capability on a session
	// (e.g. AddSessionResources returning ErrSessionNotFound).
	registerFailure metric.Int64Counter
}

func newDiscoveryMetrics(mp metric.MeterProvider) discoveryMetrics {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(proxiedToolsMeterName)

	probeSuccess, _ := meter.Int64Counter("mcp.discovery.probe_success",
		metric.WithDescription("Number of MCP-support probes that found a datasource is MCP-enabled"),
		metric.WithUnit("{probe}"),
	)
	probeDeterministicFailure, _ := meter.Int64Counter("mcp.discovery.probe_deterministic_failure",
		metric.WithDescription("Number of MCP-support probes that got a clean non-retryable response (e.g. 404)"),
		metric.WithUnit("{probe}"),
	)
	probeTransientFailure, _ := meter.Int64Counter("mcp.discovery.probe_transient_failure",
		metric.WithDescription("Number of MCP-support probes that exhausted retries on a transient error"),
		metric.WithUnit("{probe}"),
	)
	probeRetries, _ := meter.Int64Counter("mcp.discovery.probe_retries",
		metric.WithDescription("Number of MCP-support probe attempts issued as a retry"),
		metric.WithUnit("{attempt}"),
	)
	connectSuccess, _ := meter.Int64Counter("mcp.discovery.connect_success",
		metric.WithDescription("Number of proxied MCP client connections established"),
		metric.WithUnit("{connection}"),
	)
	connectDeterministicFailure, _ := meter.Int64Counter("mcp.discovery.connect_deterministic_failure",
		metric.WithDescription("Number of proxied MCP client connections that failed for a non-retryable reason"),
		metric.WithUnit("{connection}"),
	)
	connectTransientFailure, _ := meter.Int64Counter("mcp.discovery.connect_transient_failure",
		metric.WithDescription("Number of proxied MCP client connections that exhausted retries on a transient error"),
		metric.WithUnit("{connection}"),
	)
	connectRetries, _ := meter.Int64Counter("mcp.discovery.connect_retries",
		metric.WithDescription("Number of proxied MCP client connect attempts issued as a retry"),
		metric.WithUnit("{attempt}"),
	)

	upstreamListFailure, _ := meter.Int64Counter("mcp.discovery.upstream_list_failure",
		metric.WithDescription("Number of upstream MCP capability listings that failed with an error other than method-not-found"),
		metric.WithUnit("{request}"),
	)
	capabilitiesRegistered, _ := meter.Int64Counter("mcp.proxied.capabilities_registered",
		metric.WithDescription("Number of proxied upstream MCP capabilities re-exposed to clients"),
		metric.WithUnit("{capability}"),
	)
	capabilitiesSkipped, _ := meter.Int64Counter("mcp.proxied.capabilities_skipped",
		metric.WithDescription("Number of proxied upstream MCP capabilities discovered but not re-exposed"),
		metric.WithUnit("{capability}"),
	)
	registerFailure, _ := meter.Int64Counter("mcp.proxied.register_failure",
		metric.WithDescription("Number of failures registering proxied MCP capabilities on a session"),
		metric.WithUnit("{registration}"),
	)

	return discoveryMetrics{
		probeSuccess:                probeSuccess,
		probeDeterministicFailure:   probeDeterministicFailure,
		probeTransientFailure:       probeTransientFailure,
		probeRetries:                probeRetries,
		connectSuccess:              connectSuccess,
		connectDeterministicFailure: connectDeterministicFailure,
		connectTransientFailure:     connectTransientFailure,
		connectRetries:              connectRetries,
		upstreamListFailure:         upstreamListFailure,
		capabilitiesRegistered:      capabilitiesRegistered,
		capabilitiesSkipped:         capabilitiesSkipped,
		registerFailure:             registerFailure,
	}
}

// recordRegistered counts n capabilities of the given kind registered in the
// given scope. n==0 is skipped so an empty registration adds no series.
func (m discoveryMetrics) recordRegistered(ctx context.Context, capability, scope string, n int) {
	if n <= 0 || m.capabilitiesRegistered == nil {
		return
	}
	m.capabilitiesRegistered.Add(ctx, int64(n), metric.WithAttributes(
		attribute.String(attrProxiedCapability, capability),
		attribute.String(attrProxiedScope, scope),
	))
}

// recordSkipped counts n capabilities of the given kind that were discovered
// upstream but not re-exposed, tagged with why.
func (m discoveryMetrics) recordSkipped(ctx context.Context, capability, reason string, n int) {
	if n <= 0 || m.capabilitiesSkipped == nil {
		return
	}
	m.capabilitiesSkipped.Add(ctx, int64(n), metric.WithAttributes(
		attribute.String(attrProxiedCapability, capability),
		attribute.String(attrProxiedSkipReason, reason),
	))
}

// recordRegisterFailure counts one failed attempt to register a capability on a
// session.
func (m discoveryMetrics) recordRegisterFailure(ctx context.Context, capability, scope string) {
	if m.registerFailure == nil {
		return
	}
	m.registerFailure.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrProxiedCapability, capability),
		attribute.String(attrProxiedScope, scope),
	))
}

// MCPDatasourceConfig defines configuration for a datasource type that supports MCP
type MCPDatasourceConfig struct {
	Type         string
	EndpointPath string // e.g., "/api/mcp"
}

// mcpEnabledDatasources is a registry of datasource types that support MCP
var mcpEnabledDatasources = map[string]MCPDatasourceConfig{
	"tempo": {Type: "tempo", EndpointPath: "/api/mcp"},
	// Future: add other datasource types here
}

// DiscoveredDatasource represents a datasource that supports MCP
type DiscoveredDatasource struct {
	UID    string
	Name   string
	Type   string
	MCPURL string // The MCP endpoint URL
}

// discoverMCPDatasources discovers datasources that support MCP.
// Returns the list of datasources with MCP endpoints and the number of
// candidates considered (datasources of an MCP-enabled type, before probing).
func discoverMCPDatasources(ctx context.Context, logger *slog.Logger, metrics discoveryMetrics) ([]DiscoveredDatasource, int, error) {
	gc := GrafanaClientFromContext(ctx)
	if gc == nil {
		return nil, 0, fmt.Errorf("grafana client not found in context")
	}

	var discovered []DiscoveredDatasource

	// List all datasources
	resp, err := gc.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(ctx),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list datasources: %w", err)
	}

	// Get the Grafana base URL from context
	config := GrafanaConfigFromContext(ctx)
	if config.URL == "" {
		return nil, 0, fmt.Errorf("grafana url not found in context")
	}
	grafanaBaseURL := config.URL

	// Filter for datasources that support MCP and collect candidates
	type candidate struct {
		uid      string
		name     string
		dsType   string
		dsConfig MCPDatasourceConfig
	}
	var candidates []candidate
	for _, ds := range resp.Payload {
		// Check if this datasource type supports MCP
		dsConfig, supported := mcpEnabledDatasources[ds.Type]
		if !supported {
			continue
		}
		candidates = append(candidates, candidate{
			uid:      ds.UID,
			name:     ds.Name,
			dsType:   ds.Type,
			dsConfig: dsConfig,
		})
	}

	if len(candidates) == 0 {
		logger.DebugContext(ctx, "no candidate MCP datasources found")
		return nil, 0, nil
	}

	transport, err := BuildTransport(&config, nil)
	if err != nil {
		return nil, len(candidates), fmt.Errorf("failed to create transport: %w", err)
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   mcpProbeTimeout,
	}

	// Probe candidates in parallel, retrying transient failures.
	type probeResult struct {
		ds      DiscoveredDatasource
		enabled bool
	}
	results := make(chan probeResult, len(candidates))
	var wg sync.WaitGroup

	for _, c := range candidates {
		wg.Add(1)
		go func(c candidate) {
			defer wg.Done()

			probeURL := fmt.Sprintf("%s/api/datasources/proxy/uid/%s%s", grafanaBaseURL, c.uid, c.dsConfig.EndpointPath)
			typeAttr := metric.WithAttributes(attribute.String(attrDatasourceType, c.dsType))

			doProbe := func(attemptNum int) (struct{}, error) {
				if attemptNum > 1 {
					metrics.probeRetries.Add(ctx, 1, typeAttr)
				}

				probeCtx, cancel := context.WithTimeoutCause(ctx, mcpProbeTimeout,
					fmt.Errorf("timed out after %s probing MCP endpoint for datasource %s (%s) at %s (attempt %d/%d)",
						mcpProbeTimeout, c.name, c.uid, probeURL, attemptNum, mcpRetryMaxAttempts))
				defer cancel()

				// Check if the datasource instance has MCP enabled
				// We use a DELETE request to probe the MCP endpoint since:
				// - GET would start an event stream and hang
				// - POST doesn't work with the Grafana OpenAPI client
				// - DELETE returns 200 if MCP is enabled, 404 if not
				req, err := http.NewRequestWithContext(probeCtx, http.MethodDelete, probeURL, nil)
				if err != nil {
					return struct{}{}, fmt.Errorf("failed to create probe request: %w", err)
				}

				resp, err := httpClient.Do(req)
				if err != nil {
					return struct{}{}, newTransientError(contextCauseOrErr(probeCtx, err))
				}
				defer func() { _ = resp.Body.Close() }()

				switch {
				case resp.StatusCode == http.StatusOK:
					return struct{}{}, nil
				case resp.StatusCode >= 500:
					return struct{}{}, newTransientError(fmt.Errorf("probe returned server error status %d", resp.StatusCode))
				default:
					return struct{}{}, fmt.Errorf("probe returned non-OK status %d", resp.StatusCode)
				}
			}

			_, probeErr := withRetry(ctx, defaultRetryPolicy,
				fmt.Sprintf("MCP probe for datasource %s (%s)", c.name, c.uid), doProbe)

			switch {
			case probeErr == nil:
				metrics.probeSuccess.Add(ctx, 1, typeAttr)
				results <- probeResult{
					ds: DiscoveredDatasource{
						UID:    c.uid,
						Name:   c.name,
						Type:   c.dsType,
						MCPURL: probeURL,
					},
					enabled: true,
				}
			case isTransient(probeErr):
				metrics.probeTransientFailure.Add(ctx, 1, typeAttr)
				logger.WarnContext(ctx, "MCP probe failed after retries; excluding datasource from proxied tool set",
					"datasource", c.uid, "name", c.name, "type", c.dsType, "error", probeErr)
				results <- probeResult{}
			default:
				// A clean non-OK response (typically 404) just means this
				// datasource instance doesn't have MCP enabled, which is a
				// routine, expected outcome for many Tempo datasources, not a
				// failure. Debug (not Warn) keeps that from drowning out the
				// transient case above, which is the one worth an operator's
				// attention.
				metrics.probeDeterministicFailure.Add(ctx, 1, typeAttr)
				logger.DebugContext(ctx, "MCP probe determined datasource does not support MCP; excluding it",
					"datasource", c.uid, "name", c.name, "type", c.dsType, "error", probeErr)
				results <- probeResult{}
			}
		}(c)
	}

	// Wait for all probes to complete and close results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	for result := range results {
		if result.enabled {
			discovered = append(discovered, result.ds)
		}
	}

	logger.DebugContext(ctx, "discovered MCP datasources", "count", len(discovered), "candidates", len(candidates))
	return discovered, len(candidates), nil
}

// addDatasourceUidParameter adds a required datasourceUid parameter to a tool's input schema
func addDatasourceUidParameter(tool mcp.Tool, datasourceType string) mcp.Tool {
	modifiedTool := tool
	// Prefix tool name with datasource type (e.g., "tempo_traceql-search")
	modifiedTool.Name = datasourceType + "_" + tool.Name

	// Add datasourceUid to the input schema
	if modifiedTool.InputSchema.Properties == nil {
		modifiedTool.InputSchema.Properties = make(map[string]any)
	}

	modifiedTool.InputSchema.Properties["datasourceUid"] = map[string]any{
		"type":        "string",
		"description": "UID of the " + datasourceType + " datasource to query",
	}

	// Add to required fields
	modifiedTool.InputSchema.Required = append(modifiedTool.InputSchema.Required, "datasourceUid")

	return modifiedTool
}

// parseProxiedToolName extracts datasource type and original tool name from a proxied tool name
// Format: <datasource_type>_<original_tool_name>
// Returns: datasourceType, originalToolName, error
func parseProxiedToolName(toolName string) (string, string, error) {
	parts := strings.SplitN(toolName, "_", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid proxied tool name format: %s", toolName)
	}
	return parts[0], parts[1], nil
}

// proxiedResourceURIScheme is the URN namespace identifier for resources proxied
// from upstream MCP servers. The full URI form is:
//
//	urn:mcp-grafana:<datasourceType>:<datasourceUID>:<percent-encoded-original-uri>
//
// A URN was chosen over a custom scheme because it is a valid RFC 3986 URI and
// so passes mcp-go's url.ParseRequestURI validation, while keeping the original
// upstream URI recoverable for forwarding.
const proxiedResourceURIScheme = "urn:mcp-grafana"

// namespaceResourceURI rewrites an upstream resource URI to a namespaced URN
// that embeds the datasource type and UID, so multiple datasources of the same
// type can expose colliding URIs without conflict.
//
// PathEscape is used (not QueryEscape) because resource templates embed the
// upstream URI verbatim and rely on PathUnescape leaving '+' as a literal '+'
// on the read path. QueryUnescape would decode '+' to ' ', corrupting any
// upstream URI containing a literal '+' (e.g. a template like
// docs://a+b/{section}).
func namespaceResourceURI(datasourceType, datasourceUID, originalURI string) string {
	return fmt.Sprintf("%s:%s:%s:%s",
		proxiedResourceURIScheme,
		datasourceType,
		datasourceUID,
		url.PathEscape(originalURI),
	)
}

// parseNamespacedResourceURI is the inverse of namespaceResourceURI, returning
// the datasource type, datasource UID, and original upstream URI. It errors when
// the URI is not of the proxied URN form.
//
// PathUnescape mirrors namespaceResourceURI's PathEscape, so a literal '+' in
// the original URI (which resource templates embed verbatim rather than escaped)
// survives the round trip.
func parseNamespacedResourceURI(uri string) (datasourceType, datasourceUID, originalURI string, err error) {
	if !strings.HasPrefix(uri, proxiedResourceURIScheme+":") {
		return "", "", "", fmt.Errorf("not a proxied resource URI: %s", uri)
	}
	rest := strings.TrimPrefix(uri, proxiedResourceURIScheme+":")
	// rest = <datasourceType>:<datasourceUID>:<encoded-original-uri>
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid proxied resource URI format: %s", uri)
	}
	original, err := url.PathUnescape(parts[2])
	if err != nil {
		return "", "", "", fmt.Errorf("failed to decode original URI in %s: %w", uri, err)
	}
	return parts[0], parts[1], original, nil
}

// namespacePromptName prefixes an upstream prompt name with the datasource type,
// matching the tool naming scheme: <datasourceType>_<originalName>. The
// datasource UID is supplied at call time via an injected argument rather than
// encoded in the name, because UIDs may contain underscores, which would make a
// positional encoding ambiguous to parse.
func namespacePromptName(datasourceType, originalName string) string {
	return fmt.Sprintf("%s_%s", datasourceType, originalName)
}

// parseProxiedPromptName is the inverse of namespacePromptName, returning the
// datasource type and the original upstream prompt name.
func parseProxiedPromptName(name string) (datasourceType, originalName string, err error) {
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid proxied prompt name format: %s", name)
	}
	return parts[0], parts[1], nil
}

// addDatasourceUidPromptArgument returns a copy of the prompt with a required
// datasourceUid argument prepended, mirroring addDatasourceUidParameter for
// tools.
func addDatasourceUidPromptArgument(p mcp.Prompt, datasourceType string) mcp.Prompt {
	out := p
	out.Arguments = append([]mcp.PromptArgument{{
		Name:        "datasourceUid",
		Description: "UID of the " + datasourceType + " datasource to query",
		Required:    true,
	}}, p.Arguments...)
	return out
}

// namespaceResource returns a copy of an upstream resource with its URI
// rewritten to the proxied URN form and its human-readable name suffixed with
// the datasource name, to disambiguate identical resources coming from several
// datasources of the same type.
func namespaceResource(r mcp.Resource, client *ProxiedClient) mcp.Resource {
	out := r
	out.URI = namespaceResourceURI(client.DatasourceType, client.DatasourceUID, r.URI)
	out.Name = fmt.Sprintf("%s (%s)", r.Name, client.DatasourceName)
	return out
}

// namespaceResourceTemplate is the resource-template counterpart of
// namespaceResource: the upstream URI template is wrapped in the proxied URN
// form so a client's substitution still produces a valid namespaced URI.
//
// ok is false when the template cannot be safely namespaced, either because it
// has no URI template at all (mcp-go's AddResourceTemplates dereferences
// URITemplate without a nil check and would panic) or because the namespaced
// form is not a valid URI template. Callers MUST skip such an entry: registering
// it with its un-namespaced template would advertise a URI this server cannot
// route back to any datasource.
func namespaceResourceTemplate(t mcp.ResourceTemplate, client *ProxiedClient) (out mcp.ResourceTemplate, ok bool) {
	if t.URITemplate == nil {
		return mcp.ResourceTemplate{}, false
	}
	// A URI template body cannot be percent-encoded without breaking its
	// variable expressions, so it is embedded verbatim. That is safe because a
	// template only ever produces URIs that the client hands back to us in
	// resources/read, where the namespaced URI is re-parsed (and PathUnescape
	// leaves an unescaped body unchanged).
	raw := fmt.Sprintf("%s:%s:%s:%s",
		proxiedResourceURIScheme,
		client.DatasourceType,
		client.DatasourceUID,
		t.URITemplate.Raw(),
	)
	parsed, err := uritemplate.New(raw)
	if err != nil {
		return mcp.ResourceTemplate{}, false
	}
	out = t
	out.URITemplate = &mcp.URITemplate{Template: parsed}
	out.Name = fmt.Sprintf("%s (%s)", t.Name, client.DatasourceName)
	return out, true
}

// namespacePrompt rewrites an upstream prompt's name to embed the datasource
// type and adds the required datasourceUid argument that callers supply at
// invocation time.
func namespacePrompt(p mcp.Prompt, client *ProxiedClient) mcp.Prompt {
	out := addDatasourceUidPromptArgument(p, client.DatasourceType)
	out.Name = namespacePromptName(client.DatasourceType, p.Name)
	return out
}

// proxiedToolSetKey uniquely identifies a set of proxied clients/tools by every
// piece of GrafanaConfig that BuildTransport / NewProxiedClient consume to build
// and authenticate the connection. Sessions that resolve to the same key share a
// single proxiedToolSet, so the number of live sessions no longer multiplies
// memory: proxied clients and rewritten tools are built and stored once per
// distinct credential set rather than once per session.
//
// The key MUST include everything that changes the built client, otherwise two
// sessions differing only in an omitted field would share a client built with
// the first session's material (credential bleed across users/tenants):
//   - AccessToken and IDToken: Grafana Cloud per-user / on-behalf-of auth.
//   - TLS material (cert/key/CA/skip-verify): mutual-TLS client identity.
//   - timeout: GrafanaConfig.Timeout is baked into the built transport.
//   - extraHeaders: forwarded/extra headers land in GrafanaConfig.ExtraHeaders
//     and are sent on every request; a serialized, unambiguous form is used
//     because a map is neither comparable nor usable as a struct field of a map
//     key.
//
// GrafanaConfig.BaseTransport is intentionally NOT part of the key: it is an
// http.RoundTripper (an interface value that is not reliably comparable) and is
// process-constant (set once at server construction, never per request/session),
// so it cannot differ between two sessions and needs no differentiation.
type proxiedToolSetKey struct {
	url           string
	apiKey        string
	accessToken   string
	idToken       string
	orgID         int64
	timeout       time.Duration
	basicAuthUser string
	basicAuthPass string
	tlsCertFile   string
	tlsKeyFile    string
	tlsCAFile     string
	tlsSkipVerify bool
	extraHeaders  string // sorted, unambiguously-encoded ExtraHeaders
}

// proxiedToolSetKeyFromContext builds a proxiedToolSetKey from the GrafanaConfig
// carried in the context.
func proxiedToolSetKeyFromContext(ctx context.Context) proxiedToolSetKey {
	config := GrafanaConfigFromContext(ctx)
	key := proxiedToolSetKey{
		url:          config.URL,
		apiKey:       config.APIKey,
		accessToken:  config.AccessToken,
		idToken:      config.IDToken,
		orgID:        config.OrgID,
		timeout:      config.Timeout,
		extraHeaders: serializeHeaders(config.ExtraHeaders),
	}
	if config.BasicAuth != nil {
		key.basicAuthUser = config.BasicAuth.Username()
		key.basicAuthPass, _ = config.BasicAuth.Password()
	}
	if tls := config.TLSConfig; tls != nil {
		key.tlsCertFile = tls.CertFile
		key.tlsKeyFile = tls.KeyFile
		key.tlsCAFile = tls.CAFile
		key.tlsSkipVerify = tls.SkipVerify
	}
	return key
}

// serializeHeaders renders a header map into a deterministic, injective string
// so it can be part of a comparable cache key. Names and values are individually
// percent-escaped before joining, so no combination of names/values can collide
// with a different map: e.g. {"H":"a,b=c"} and {"H":"a","b":"c"} encode
// distinctly. (client_cache.go's cacheKeyFromRequest uses a plainer, ambiguous
// join; this key must not share that flaw.)
func serializeHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, k := range names {
		sb.WriteString(url.QueryEscape(k))
		sb.WriteByte('=')
		sb.WriteString(url.QueryEscape(headers[k]))
		sb.WriteByte('&')
	}
	return sb.String()
}

// String returns a redacted string representation for logging: secret-bearing
// fields (apiKey, accessToken, idToken, basicAuthPass) are reduced to a present/
// absent bool, never their value.
func (k proxiedToolSetKey) String() string {
	return fmt.Sprintf("url=%s apiKey=%t accessToken=%t idToken=%t orgID=%d timeout=%s basicAuth=%t tlsCert=%t tlsCA=%t tlsSkipVerify=%t extraHeaders=%t",
		k.url, k.apiKey != "", k.accessToken != "", k.idToken != "", k.orgID, k.timeout, k.basicAuthUser != "",
		k.tlsCertFile != "", k.tlsCAFile != "", k.tlsSkipVerify, k.extraHeaders != "")
}

// LogValue makes proxiedToolSetKey a slog.LogValuer so that logging it (e.g.
// slog "key", set.key) emits the redacted String() form. slog does NOT honor
// fmt.Stringer for Any values, so without this the raw struct fields (including
// the secret apiKey/accessToken/idToken/basicAuthPass) would be reflected into
// logs.
func (k proxiedToolSetKey) LogValue() slog.Value {
	return slog.StringValue(k.String())
}

// proxiedToolSet is the shared, credential-keyed result of discovering and
// connecting to proxied MCP datasources. It is reference counted: sessions
// attach to it (incrementing refs) and detach on teardown (decrementing refs).
// Its clients are closed and it is dropped from the cache only when the last
// session detaches (refs reaches zero) AND no tool call is in flight against it.
//
// Lifecycle and memory model. A placeholder is inserted into the cache under
// proxiedSetsMu (with refs already accounting for the first attaching session)
// BEFORE the expensive discovery/connection runs, so a concurrent session for
// the same key reuses it rather than building a second copy. The build itself
// runs WITHOUT any lock and mutates only local variables; its results are
// published into clients/tools/toolToDatasources in a single critical section
// under proxiedSetsMu (see runProxiedToolSetBuild). This "build then publish"
// ordering is essential: teardown (maybeCloseProxiedToolSetLocked) ranges over
// clients while holding proxiedSetsMu, so clients must never be written outside
// that lock while the set is reachable, otherwise a concurrent build write and
// teardown read would be a "concurrent map iteration and map write" fatal error
// that recover() cannot catch.
//
// All the fields below are guarded by ToolManager.proxiedSetsMu.
type proxiedToolSet struct {
	// key identifies this set in ToolManager.proxiedSets. Stored so teardown can
	// remove the cache entry when releasing by set pointer, and so a rebuild
	// after a failed build never removes a newer entry for the same key.
	key proxiedToolSetKey

	// ready is closed once the build has finished (whether it succeeded, failed,
	// or was abandoned because all sessions left mid-build). Sessions that attach
	// to an in-progress set wait on it before reading built/failed/tools.
	ready chan struct{}

	// built reports that the build completed and published its results into the
	// clients/tools maps. Until built is true the maps are empty placeholders and
	// must not be treated as the set's contents. A build that discovers zero MCP
	// datasources is still a successful build: built is true and the maps are
	// legitimately empty (see failed for the transient case).
	built bool
	// failed reports that the build hit a TRANSIENT error (discovery error,
	// context cancellation, or panic). A failed set is removed from the cache so
	// the next session rebuilds and retries. It is NOT set for a successful build
	// that simply found no MCP datasources: that empty result is stable, so the
	// set is published (built=true, failed=false, empty maps) and cached, and
	// sessions do not re-run discovery on every hook.
	failed bool

	// clients keyed by datasourceType_datasourceUID. Empty until built is true;
	// published in one critical section and immutable afterwards.
	clients map[string]*ProxiedClient
	// tools is the deduplicated, schema-rewritten set of proxied tools. Empty
	// until built is true; immutable afterwards.
	tools []mcp.Tool
	// toolToDatasources maps a proxied tool name to the datasource keys that
	// support it. Empty until built is true; immutable afterwards.
	toolToDatasources map[string][]string
	// resources and resourceTemplates are the upstream resources and resource
	// templates, already rewritten into the namespaced URN form clients see.
	// Empty until built is true; immutable afterwards.
	resources         []mcp.Resource
	resourceTemplates []mcp.ResourceTemplate
	// prompts is the upstream prompt set, already namespaced. It is built but
	// NOT registered on HTTP/SSE transports: mcp-go has no AddSessionPrompts, so
	// prompts cannot be scoped to one session, and registering them server-wide
	// would leak one tenant's prompts to another. Kept so the gap is countable
	// and reportable rather than invisible (see skipReasonNoSessionPrompts).
	prompts []mcp.Prompt

	// refs is the number of live sessions currently attached to this set.
	refs int
	// inFlight is the number of tool calls currently executing against this
	// set's clients. Teardown must not Close clients while any call is in flight.
	inFlight int
	// closed reports that the set's clients have been Closed, so Close runs at
	// most once.
	closed bool
}

// ToolManager manages proxied tools (either per-session or server-wide)
type ToolManager struct {
	sm     *SessionManager
	server *server.MCPServer
	logger *slog.Logger

	// Whether to enable proxied tools.
	enableProxiedTools bool

	// For stdio transport: store clients at manager level (single-tenant).
	// These will be unused for HTTP/SSE transports.
	serverMode    bool // true if using server-wide tools (stdio), false for per-session (HTTP/SSE)
	serverClients map[string]*ProxiedClient
	clientsMutex  sync.RWMutex

	// For HTTP/SSE transport: shared, credential-keyed proxied tool sets.
	// Sessions with identical credentials share a single entry, so memory no
	// longer scales with the number of live sessions. Entries are reference
	// counted and their clients closed when the last session detaches. The
	// entry for a key is inserted before the (slow) build runs, so concurrent
	// sessions for the same key reuse it rather than each building a copy.
	proxiedSets   map[proxiedToolSetKey]*proxiedToolSet
	proxiedSetsMu sync.Mutex
	// buildSet performs the discovery + connection + schema rewrite for a key and
	// returns the results in LOCAL maps (it must mutate no shared state, so it can
	// run without a lock while the placeholder set is already reachable). It
	// returns an error when the set could not be built into a usable state. It
	// defaults to buildProxiedToolSet and is a field only so tests can inject a
	// fake builder that avoids real discovery/network I/O.
	buildSet func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error)

	// metrics holds OTel instruments for discovery/connect observability.
	metrics discoveryMetrics
	// meterProvider is the metric.MeterProvider used to build metrics, set via
	// WithToolManagerMeterProvider.
	meterProvider metric.MeterProvider
}

// NewToolManager creates a new ToolManager
func NewToolManager(sm *SessionManager, mcpServer *server.MCPServer, opts ...toolManagerOption) *ToolManager {
	tm := &ToolManager{
		sm:            sm,
		server:        mcpServer,
		serverClients: make(map[string]*ProxiedClient),
		proxiedSets:   make(map[proxiedToolSetKey]*proxiedToolSet),
	}
	for _, opt := range opts {
		opt(tm)
	}
	tm.metrics = newDiscoveryMetrics(tm.meterProvider)
	if tm.logger == nil {
		tm.logger = slog.Default()
	}
	if tm.buildSet == nil {
		tm.buildSet = tm.buildProxiedToolSet
	}
	if tm.proxiedSets == nil {
		tm.proxiedSets = make(map[proxiedToolSetKey]*proxiedToolSet)
	}
	return tm
}

type toolManagerOption func(*ToolManager)

// WithProxiedTools sets whether proxied tools are enabled
func WithProxiedTools(enabled bool) toolManagerOption {
	return func(tm *ToolManager) {
		tm.enableProxiedTools = enabled
	}
}

// WithToolManagerLogger sets the logger for the ToolManager.
func WithToolManagerLogger(logger *slog.Logger) toolManagerOption {
	return func(tm *ToolManager) {
		tm.logger = logger
	}
}

// WithToolManagerMeterProvider sets the metric.MeterProvider used to create
// the ToolManager's discovery/connect OTel instruments. If unset (or passed
// as nil), the ToolManager falls back to otel.GetMeterProvider(), matching
// the pre-existing behavior. Callers embedding mcp-grafana as a library and
// running with a non-global MeterProvider (e.g. because the process resets
// the global provider to a noop for unrelated reasons) should pass their own
// provider here so these metrics actually reach a scrapeable registry.
func WithToolManagerMeterProvider(mp metric.MeterProvider) toolManagerOption {
	return func(tm *ToolManager) {
		tm.meterProvider = mp
	}
}

// loggerFromCtx returns the logger from the context's GrafanaConfig if available,
// otherwise falls back to the ToolManager's logger.
func (tm *ToolManager) loggerFromCtx(ctx context.Context) *slog.Logger {
	config := GrafanaConfigFromContext(ctx)
	if config.Logger != nil {
		return config.Logger
	}
	return tm.logger
}

// InitializeAndRegisterServerTools discovers datasources and registers their
// proxied tools, resources, resource templates, and prompts on the server (for
// stdio transport). This should be called once at server startup for
// single-tenant stdio servers.
//
// Unlike the per-session path, prompts ARE registered here: stdio is
// single-tenant, so a server-wide prompt cannot leak across callers.
func (tm *ToolManager) InitializeAndRegisterServerTools(ctx context.Context) error {
	if !tm.enableProxiedTools {
		return nil
	}

	// Mark as server mode (stdio transport)
	tm.serverMode = true

	logger := tm.loggerFromCtx(ctx)

	// Discover datasources with MCP support
	discovered, _, err := discoverMCPDatasources(ctx, logger, tm.metrics)
	if err != nil {
		return fmt.Errorf("failed to discover MCP datasources: %w", err)
	}

	if len(discovered) == 0 {
		logger.InfoContext(ctx, "no MCP datasources discovered")
		return nil
	}

	// Connect to each datasource and store in manager
	tm.clientsMutex.Lock()
	for _, ds := range discovered {
		client, err := NewProxiedClient(ctx, ds.UID, ds.Name, ds.Type, ds.MCPURL,
			withProxiedClientMetrics(tm.metrics))
		if err != nil {
			logger.ErrorContext(ctx, "failed to create proxied client", "datasource", ds.UID, "error", err)
			continue
		}
		key := ds.Type + "_" + ds.UID
		tm.serverClients[key] = client
	}
	clientCount := len(tm.serverClients)
	tm.clientsMutex.Unlock()

	if clientCount == 0 {
		logger.WarnContext(ctx, "no proxied clients created")
		return nil
	}

	logger.InfoContext(ctx, "connected to proxied MCP servers", "datasources", clientCount)

	// Collect all unique tools plus the namespaced resources, resource
	// templates, and prompts. Namespacing keys (tool/prompt name, resource URI)
	// embed the datasource type and UID, so several datasources of the same type
	// coexist without collisions.
	resourceHandler := NewProxiedResourceHandler(tm.sm, tm)
	promptHandler := NewProxiedPromptHandler(tm.sm, tm)

	tm.clientsMutex.RLock()
	toolMap := make(map[string]mcp.Tool)
	var resources []server.ServerResource
	var resourceTemplates []server.ServerResourceTemplate
	var prompts []server.ServerPrompt
	templatesSkipped := 0
	for _, client := range tm.serverClients {
		for _, tool := range client.ListTools() {
			toolName := client.DatasourceType + "_" + tool.Name
			if _, exists := toolMap[toolName]; !exists {
				modifiedTool := addDatasourceUidParameter(tool, client.DatasourceType)
				toolMap[toolName] = modifiedTool
			}
		}
		for _, res := range client.ListResources() {
			resources = append(resources, server.ServerResource{
				Resource: namespaceResource(res, client),
				Handler:  resourceHandler.Handle,
			})
		}
		for _, tmpl := range client.ListResourceTemplates() {
			ns, ok := namespaceResourceTemplate(tmpl, client)
			if !ok {
				templatesSkipped++
				logger.WarnContext(ctx, "skipping upstream resource template that cannot be namespaced",
					"datasource", client.DatasourceUID, "type", client.DatasourceType, "name", tmpl.Name)
				continue
			}
			resourceTemplates = append(resourceTemplates, server.ServerResourceTemplate{
				Template: ns,
				Handler:  resourceHandler.Handle,
			})
		}
		for _, prompt := range client.ListPrompts() {
			prompts = append(prompts, server.ServerPrompt{
				Prompt:  namespacePrompt(prompt, client),
				Handler: promptHandler.Handle,
			})
		}
	}
	tm.clientsMutex.RUnlock()

	// Register everything on the server (not per-session).
	for toolName, tool := range toolMap {
		handler := NewProxiedToolHandler(tm.sm, tm, toolName)
		tm.server.AddTool(tool, handler.Handle)
	}
	if len(resources) > 0 {
		tm.server.AddResources(resources...)
	}
	if len(resourceTemplates) > 0 {
		tm.server.AddResourceTemplates(resourceTemplates...)
	}
	if len(prompts) > 0 {
		tm.server.AddPrompts(prompts...)
	}

	tm.metrics.recordRegistered(ctx, capabilityTools, scopeServer, len(toolMap))
	tm.metrics.recordRegistered(ctx, capabilityResources, scopeServer, len(resources))
	tm.metrics.recordRegistered(ctx, capabilityResourceTemplates, scopeServer, len(resourceTemplates))
	tm.metrics.recordRegistered(ctx, capabilityPrompts, scopeServer, len(prompts))
	tm.metrics.recordSkipped(ctx, capabilityResourceTemplates, skipReasonUnnamespaceableTemplate, templatesSkipped)

	logger.InfoContext(ctx, "registered proxied capabilities on server",
		"tools", len(toolMap),
		"resources", len(resources),
		"resource_templates", len(resourceTemplates),
		"templates_skipped", templatesSkipped,
		"prompts", len(prompts))
	return nil
}

// buildStats summarizes how a build's candidate datasources fared, for the
// "built proxied tool set" summary log. Zero-value-safe: builders that don't
// populate it (e.g. test seams) simply report zeros.
type buildStats struct {
	candidates       int // datasources of an MCP-enabled type, before probing
	discovered       int // candidates that passed the MCP probe
	connectFailed    int // discovered datasources that failed to connect (after retries)
	templatesSkipped int // upstream resource templates that could not be namespaced
}

// builtProxiedTools is the result of a build, held in local variables while the
// build runs so that nothing shared is mutated without the cache lock. It is
// published into a proxiedToolSet in a single critical section.
type builtProxiedTools struct {
	clients           map[string]*ProxiedClient
	tools             []mcp.Tool
	toolToDatasources map[string][]string
	resources         []mcp.Resource
	resourceTemplates []mcp.ResourceTemplate
	prompts           []mcp.Prompt
	stats             buildStats
}

// buildProxiedToolSet discovers datasources, connects to them, and returns the
// built clients/tools/toolToDatasources in LOCAL maps. It mutates no shared
// state, so it is safe to run without any lock while the placeholder set is
// already reachable by teardown. It returns an error when the set could not be
// built into a usable state (discovery failed or was cancelled). A successful
// discovery that finds zero MCP datasources is not an error: it is a legitimate
// "no proxied tools" result (an empty build, handled by the caller).
func (tm *ToolManager) buildProxiedToolSet(ctx context.Context, logger *slog.Logger) (built builtProxiedTools, err error) {
	built = builtProxiedTools{
		clients:           make(map[string]*ProxiedClient),
		toolToDatasources: make(map[string][]string),
	}

	// Convert a panic into an error while KEEPING the clients connected so far in
	// the named return, so the caller's non-publish path closes them instead of
	// leaking their remote connections. (The caller also recovers, but only the
	// caller's own frame; a panic here would otherwise discard `built` entirely.)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic building proxied tool set: %v", r)
			logger.ErrorContext(ctx, "panic building proxied tool set", "error", r)
		}
	}()

	// Discover datasources with MCP support.
	discovered, candidateCount, err := discoverMCPDatasources(ctx, logger, tm.metrics)
	if err != nil {
		logger.ErrorContext(ctx, "failed to discover MCP datasources", "error", err)
		return built, fmt.Errorf("failed to discover MCP datasources: %w", err)
	}

	// Connect to each discovered datasource in parallel, retrying transient
	// failures, mirroring the probe step's concurrency pattern in
	// discoverMCPDatasources. A failed connect is non-fatal to the overall
	// build: it just excludes that datasource.
	type connectResult struct {
		key    string
		client *ProxiedClient
		failed bool
	}
	connectResults := make(chan connectResult, len(discovered))
	var connectWg sync.WaitGroup

	for _, ds := range discovered {
		connectWg.Add(1)
		go func(ds DiscoveredDatasource) {
			defer connectWg.Done()

			typeAttr := metric.WithAttributes(attribute.String(attrDatasourceType, ds.Type))
			description := fmt.Sprintf("connect to MCP server for datasource %s (%s)", ds.Name, ds.UID)

			// Connect work now runs in its own goroutine (parallelized, unlike
			// the sequential loop this replaced), so a panic here would
			// otherwise crash the whole process instead of being turned into
			// an error by buildProxiedToolSet's top-level recover, which only
			// guards its own goroutine. Recover here too, closing any client
			// that connected before the panic so it isn't leaked, and report
			// this candidate as failed rather than letting the panic escape.
			var client *ProxiedClient
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorContext(ctx, "panic connecting to proxied MCP client; excluding datasource",
						"datasource", ds.UID, "name", ds.Name, "type", ds.Type, "panic", r)
					tm.metrics.connectDeterministicFailure.Add(ctx, 1, typeAttr)
					if client != nil {
						if err := client.Close(); err != nil {
							logger.ErrorContext(ctx, "failed to close proxied client after panic", "datasource", ds.UID, "error", err)
						}
					}
					connectResults <- connectResult{failed: true}
				}
			}()

			var connectErr error
			client, connectErr = withRetry(ctx, defaultRetryPolicy, description,
				func(attemptNum int) (*ProxiedClient, error) {
					if attemptNum > 1 {
						tm.metrics.connectRetries.Add(ctx, 1, typeAttr)
					}
					c, err := NewProxiedClient(ctx, ds.UID, ds.Name, ds.Type, ds.MCPURL,
						withProxiedClientMetrics(tm.metrics))
					if err != nil {
						return nil, classifyConnectError(err)
					}
					return c, nil
				})

			if connectErr != nil {
				if isTransient(connectErr) {
					tm.metrics.connectTransientFailure.Add(ctx, 1, typeAttr)
				} else {
					tm.metrics.connectDeterministicFailure.Add(ctx, 1, typeAttr)
				}
				logger.WarnContext(ctx, "failed to create proxied client after retries; excluding datasource from proxied tool set",
					"datasource", ds.UID, "name", ds.Name, "type", ds.Type, "error", connectErr)
				connectResults <- connectResult{failed: true}
				return
			}

			tm.metrics.connectSuccess.Add(ctx, 1, typeAttr)
			connectResults <- connectResult{key: ds.Type + "_" + ds.UID, client: client}
		}(ds)
	}

	go func() {
		connectWg.Wait()
		close(connectResults)
	}()

	connectFailed := 0
	for r := range connectResults {
		if r.failed {
			connectFailed++
			continue
		}
		built.clients[r.key] = r.client
	}

	// Collect unique tools and track which datasources support each one, and
	// namespace the resources, resource templates, and prompts each upstream
	// exposes. Namespacing keys (tool/prompt name, resource URI) embed the
	// datasource type and UID, so several datasources of the same type coexist
	// without collisions.
	toolMap := make(map[string]mcp.Tool) // unique tools by name
	templatesSkipped := 0
	for key, client := range built.clients {
		for _, tool := range client.ListTools() {
			// Tool name format: datasourceType_originalToolName (e.g., "tempo_traceql-search").
			toolName := client.DatasourceType + "_" + tool.Name
			if _, exists := toolMap[toolName]; !exists {
				toolMap[toolName] = addDatasourceUidParameter(tool, client.DatasourceType)
			}
			built.toolToDatasources[toolName] = append(built.toolToDatasources[toolName], key)
		}

		for _, res := range client.ListResources() {
			built.resources = append(built.resources, namespaceResource(res, client))
		}
		for _, tmpl := range client.ListResourceTemplates() {
			ns, ok := namespaceResourceTemplate(tmpl, client)
			if !ok {
				templatesSkipped++
				logger.WarnContext(ctx, "skipping upstream resource template that cannot be namespaced",
					"datasource", client.DatasourceUID, "type", client.DatasourceType, "name", tmpl.Name)
				continue
			}
			built.resourceTemplates = append(built.resourceTemplates, ns)
		}
		for _, prompt := range client.ListPrompts() {
			built.prompts = append(built.prompts, namespacePrompt(prompt, client))
		}
	}
	for _, tool := range toolMap {
		built.tools = append(built.tools, tool)
	}

	built.stats = buildStats{
		candidates:       candidateCount,
		discovered:       len(discovered),
		connectFailed:    connectFailed,
		templatesSkipped: templatesSkipped,
	}
	tm.metrics.recordSkipped(ctx, capabilityResourceTemplates, skipReasonUnnamespaceableTemplate, templatesSkipped)

	return built, nil
}

// attachProxiedToolSet find-or-inserts the shared proxiedToolSet for the given
// key, takes one reference for the session, AND binds the set to the session,
// all in a single critical section. Binding happens under state.mutex while
// proxiedSetsMu is still held, so from the moment the reference exists the
// session can already release it: there is no window in which a reference is
// held but no teardown path can find it. (Teardown decrements under
// proxiedSetsMu, which this holds, so it cannot run until attach returns.)
// needsBuild reports whether this caller is the first for the key and must
// therefore run the build; every other caller waits on set.ready.
//
// Lock order here is proxiedSetsMu then state.mutex. No other path takes
// state.mutex and then proxiedSetsMu, so this nesting cannot deadlock.
func (tm *ToolManager) attachProxiedToolSet(state *SessionState, key proxiedToolSetKey) (set *proxiedToolSet, needsBuild bool) {
	tm.proxiedSetsMu.Lock()
	defer tm.proxiedSetsMu.Unlock()

	if existing, ok := tm.proxiedSets[key]; ok {
		existing.refs++
		set, needsBuild = existing, false
	} else {
		set = &proxiedToolSet{
			key:               key,
			ready:             make(chan struct{}),
			clients:           make(map[string]*ProxiedClient),
			toolToDatasources: make(map[string][]string),
			refs:              1,
		}
		tm.proxiedSets[key] = set
		needsBuild = true
	}

	state.mutex.Lock()
	state.proxiedSet = set
	state.proxiedSetReleased = false
	state.mutex.Unlock()

	return set, needsBuild
}

// runProxiedToolSetBuild builds the set (first caller only) and unblocks
// waiters. The build runs with NO lock into local maps; its results are
// published into the set in a single critical section under proxiedSetsMu,
// together with close(ready). This build-then-publish ordering is what keeps
// teardown (which ranges over set.clients under the same lock) from ever
// observing a half-written map.
//
// The build runs under a context detached from cancellation (context.WithoutCancel)
// so that a first client disconnecting mid-discovery does not cancel discovery
// for the other sessions waiting on the same set. ready is always closed, even
// on panic, so concurrent waiters never block indefinitely.
//
// Outcome handling, three cases:
//   - TRANSIENT ERROR (buildErr != nil: discovery error, context cancel, panic):
//     the set is marked failed and removed from the cache so the next session
//     rebuilds and retries, rather than leaving a poisoned entry cached.
//   - EMPTY SUCCESS (buildErr == nil, zero MCP datasources discovered): a stable
//     "this instance has no proxied tools" result. The set is published with
//     empty maps (built=true, failed=false) and KEPT in the cache, so sessions do
//     not re-run full discovery on every hook. Only a real config change (a new
//     datasource) surfaces after the cached set is torn down and rebuilt.
//   - SUCCESS WITH CLIENTS: published and cached as usual.
//
// Teardown-during-build: if every session left while the build ran (refs==0 at
// publish time), the freshly-built local clients are closed right here and never
// published, so nothing leaks and nothing is used after close.
func (tm *ToolManager) runProxiedToolSetBuild(ctx context.Context, set *proxiedToolSet, logger *slog.Logger) {
	buildCtx := context.WithoutCancel(ctx)

	// buildSet is expected to convert its own panics into an error while
	// returning any clients it already connected (buildProxiedToolSet does this
	// via named returns), so a panic's partial clients are reaped by the
	// non-publish path below. This outer recover is a last-resort guard that only
	// fires if a builder panics past its own recovery: it keeps this goroutine and
	// any ready-waiters alive by turning the panic into a failed build. In that
	// escaped-panic case built is the zero value, so a builder that connects
	// clients MUST recover internally to avoid leaking them.
	var built builtProxiedTools
	var buildErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				buildErr = fmt.Errorf("panic building proxied tool set: %v", r)
				logger.ErrorContext(ctx, "panic building proxied tool set", "key", set.key, "panic", r)
			}
		}()
		built, buildErr = tm.buildSet(buildCtx, logger)
	}()

	// Only a real error is transient/failed. A successful build that found zero
	// MCP datasources is a stable empty result and must be cached (built, empty),
	// not de-cached, so sessions do not re-discover on every hook.
	failed := buildErr != nil

	tm.proxiedSetsMu.Lock()

	switch {
	case set.refs == 0:
		// Every session left while we built. Do not publish the live clients;
		// close them here (once) and mark the set closed/failed so any late
		// arrival treats it as unusable. The entry was already removed from the
		// cache by the last release (delete-if-still-present is a no-op if a
		// rebuild replaced it), but drop it defensively.
		set.failed = true
		set.closed = true
		if tm.proxiedSets[set.key] == set {
			delete(tm.proxiedSets, set.key)
		}
	case failed:
		set.failed = true
		if tm.proxiedSets[set.key] == set {
			delete(tm.proxiedSets, set.key)
		}
	default:
		// Publish the build results (which may be legitimately empty). After this
		// the maps are immutable and safe to read under the lock (teardown) or
		// after <-ready (session use).
		set.clients = built.clients
		set.tools = built.tools
		set.toolToDatasources = built.toolToDatasources
		set.resources = built.resources
		set.resourceTemplates = built.resourceTemplates
		set.prompts = built.prompts
		set.built = true
	}
	abandoned := set.refs == 0
	published := !abandoned && !failed
	size := len(tm.proxiedSets)
	tm.proxiedSetsMu.Unlock()

	close(set.ready)

	// On any non-publish outcome the freshly-built clients were NOT stored on the
	// set, so no teardown path can observe or close them: close them here (once,
	// outside the lock) or their remote connections leak. This covers both the
	// abandoned case (all sessions left mid-build) and the failed case where the
	// builder connected some clients before erroring or panicking.
	if !published {
		tm.closeProxiedClients(built.clients)
	}

	switch {
	case abandoned:
		logger.InfoContext(ctx, "proxied tool set abandoned during build; closed clients without publishing", "key", set.key)
		return
	case failed:
		logger.InfoContext(ctx, "proxied tool set build failed; not caching", "key", set.key, "error", buildErr)
		return
	}
	logger.InfoContext(ctx, "built proxied tool set", "key", set.key,
		"candidates", built.stats.candidates, "discovered", built.stats.discovered, "connect_failed", built.stats.connectFailed,
		"datasources", len(set.clients), "tools", len(set.tools),
		"resources", len(set.resources), "resource_templates", len(set.resourceTemplates),
		"templates_skipped", built.stats.templatesSkipped, "prompts", len(set.prompts),
		"cache_size", size)
}

// releaseProxiedToolSet decrements a set's reference count and, once no session
// references it (refs==0) and no tool call is in flight against it, closes its
// clients exactly once and drops it from the cache. It is called with the set
// the session actually holds, not re-looked-up by key, so it works even for a
// failed set that was already removed from the cache (teardown of a session that
// attached mid-build still balances its reference).
func (tm *ToolManager) releaseProxiedToolSet(set *proxiedToolSet) {
	tm.proxiedSetsMu.Lock()
	set.refs--
	toClose := tm.takeClientsToCloseLocked(set)
	tm.proxiedSetsMu.Unlock()

	tm.closeProxiedClients(toClose)
}

// takeClientsToCloseLocked decides whether a set can be torn down (refs==0,
// inFlight==0, not already closed) and, if so, marks it closed, removes it from
// the cache, and returns its clients for the caller to Close AFTER releasing the
// lock. It returns nil when the set is still in use or already closed. Must be
// called with proxiedSetsMu held.
//
// Closing is deliberately deferred to outside the lock: ProxiedClient.Close does
// network I/O, and proxiedSetsMu serializes attach/release/acquire for ALL
// credential keys, so closing under it would let a slow Close on one key stall
// unrelated sessions. Setting closed=true under the lock keeps teardown
// exactly-once (only the caller that flips closed collects the clients) and
// keeps acquireProxiedClientForCall's closed check correct (an acquire either
// runs before this selection and bumps inFlight, or sees closed afterwards).
func (tm *ToolManager) takeClientsToCloseLocked(set *proxiedToolSet) map[string]*ProxiedClient {
	if set.refs > 0 || set.inFlight > 0 || set.closed {
		return nil
	}
	set.closed = true
	// Remove from the cache if this set is still the entry for its key. A failed
	// set was already removed; a rebuild may have replaced it.
	if tm.proxiedSets[set.key] == set {
		delete(tm.proxiedSets, set.key)
	}
	return set.clients
}

// closeProxiedClients closes the given clients. It must be called WITHOUT
// proxiedSetsMu held so a slow Close cannot stall unrelated sessions.
func (tm *ToolManager) closeProxiedClients(clients map[string]*ProxiedClient) {
	for clientKey, client := range clients {
		if err := client.Close(); err != nil {
			tm.logger.Error("failed to close proxied client", "key", clientKey, "error", err)
		}
	}
}

// acquireProxiedClientForCall looks up a proxied client on the given set and
// registers an in-flight call against the set, so teardown cannot Close the
// client while the call runs. The returned release func MUST be called (deferred)
// once the call completes; it decrements the in-flight count and closes the set
// if it was the last thing keeping it alive.
func (tm *ToolManager) acquireProxiedClientForCall(set *proxiedToolSet, datasourceType, datasourceUID string) (*ProxiedClient, func(), error) {
	tm.proxiedSetsMu.Lock()
	defer tm.proxiedSetsMu.Unlock()

	if set.closed {
		return nil, nil, fmt.Errorf("datasource '%s' is no longer available", datasourceUID)
	}

	key := datasourceType + "_" + datasourceUID
	client, ok := set.clients[key]
	if !ok {
		var availableUIDs []string
		for _, c := range set.clients {
			if c.DatasourceType == datasourceType {
				availableUIDs = append(availableUIDs, c.DatasourceUID)
			}
		}
		if len(availableUIDs) > 0 {
			return nil, nil, fmt.Errorf("datasource '%s' not found. Available %s datasources: %v", datasourceUID, datasourceType, availableUIDs)
		}
		return nil, nil, fmt.Errorf("datasource '%s' not found. No %s datasources with MCP support are configured", datasourceUID, datasourceType)
	}

	set.inFlight++
	release := func() {
		tm.proxiedSetsMu.Lock()
		set.inFlight--
		toClose := tm.takeClientsToCloseLocked(set)
		tm.proxiedSetsMu.Unlock()

		tm.closeProxiedClients(toClose)
	}
	return client, release, nil
}

// InitializeAndRegisterProxiedCapabilities attaches the calling session to a
// shared, credential-keyed proxied tool set and registers that set's tools,
// resources, and resource templates on the session. The shared set is
// discovered/connected/rewritten at most once per distinct credential set, so
// multiple concurrent sessions with identical credentials reuse a single set
// instead of each building their own.
//
// It must be called from the OnBefore* hook of every entry point that can
// observe a proxied capability (list/call tools, list/read resources, list
// resource templates, list/get prompts) on HTTP/SSE transports, because the
// server advertises resource and prompt capabilities at initialize time and a
// client may therefore enumerate resources before it ever lists tools. It is
// idempotent per session.
//
// Upstream prompts are deliberately NOT registered here: mcp-go exposes no
// AddSessionPrompts, so a prompt cannot be scoped to one session, and
// registering it server-wide would leak it across tenants. They are counted and
// logged instead (skipReasonNoSessionPrompts). Tempo, the only datasource type
// with an MCP server today, exposes no prompts, so this is currently only a
// forward-looking gap.
func (tm *ToolManager) InitializeAndRegisterProxiedCapabilities(ctx context.Context, session server.ClientSession) {
	if !tm.enableProxiedTools {
		return
	}

	logger := tm.loggerFromCtx(ctx)

	sessionID := session.SessionID()
	state, exists := tm.sm.GetSession(sessionID)
	if !exists {
		// Session exists in server context but not in our SessionManager yet.
		tm.sm.CreateSession(ctx, session)
		state, exists = tm.sm.GetSession(sessionID)
		if !exists {
			logger.ErrorContext(ctx, "failed to create session in SessionManager", "sessionID", sessionID)
			return
		}
	}

	key := proxiedToolSetKeyFromContext(ctx)

	// Serialize attach/build/register for this session and allow a later retry if
	// this attempt does not end in a successful registration. proxiedInitMu is
	// held for the whole attempt: concurrent hooks for the SAME session (e.g.
	// OnBeforeListTools and OnBeforeCallTool) queue behind it, and the second one
	// sees proxiedRegistered and returns, or retries if the first failed.
	state.proxiedInitMu.Lock()
	defer state.proxiedInitMu.Unlock()

	if state.proxiedRegistered {
		return
	}

	// attachProxiedToolSet takes the reference AND binds the set to the session
	// atomically (under proxiedSetsMu), so there is no window where a reference
	// exists that teardown cannot find and release.
	set, needsBuild := tm.attachProxiedToolSet(state, key)

	// Reconcile against a teardown that raced this attach. A session may have been
	// removed from the SessionManager (client DELETE / idle sweeper / reaper)
	// between GetSession/CreateSession above and the bind inside
	// attachProxiedToolSet. If so, that RemoveSession saw proxiedSet==nil and did
	// not release, and no future teardown will fire for this (now untracked)
	// session, so the ref we just took would leak. Detect it and release exactly
	// once (releaseSessionProxiedToolSet is idempotent, so a RemoveSession that
	// instead ran AFTER our bind is handled too, with no double release). We still
	// run/await the build below so any live waiter for the same key is served.
	if !tm.sm.sessionRegistered(sessionID, state) {
		defer tm.releaseSessionProxiedToolSet(state)
	}

	if needsBuild {
		tm.runProxiedToolSetBuild(ctx, set, logger)
	} else {
		<-set.ready
	}

	// Read the published results under the lock: set.built/failed/tools are only
	// stable once ready is closed, and are guarded by proxiedSetsMu.
	tm.proxiedSetsMu.Lock()
	usable := set.built && !set.failed
	tools := set.tools
	resources := set.resources
	resourceTemplates := set.resourceTemplates
	prompts := set.prompts
	tm.proxiedSetsMu.Unlock()

	// A failed build (transient: discovery error, cancellation, or panic) was
	// de-cached. Do NOT mark this session registered: release its reference to
	// the failed set, clear the binding, and return so the next hook invocation
	// for this session retries a fresh attach (which triggers a fresh build).
	// This keeps the failure transient for the session, matching the cache's
	// behavior for later sessions.
	//
	// A usable build with zero tools is NOT a failure: either the instance has no
	// MCP datasources, or its datasources exposed no tools. That is a stable,
	// cached result, so the session keeps its reference and is marked registered
	// (there is simply nothing to register); it must not retry, so repeated hooks
	// do not re-run discovery.
	if !usable {
		tm.releaseSessionProxiedToolSet(state)
		return
	}

	if len(tools) > 0 {
		// Register the shared tools on this session. AddSessionTools is
		// per-session SDK bookkeeping; it references the shared (now immutable)
		// mcp.Tool values directly, with no per-session deep copy, JSON decode, or
		// client dial.
		serverTools := make([]server.ServerTool, 0, len(tools))
		for _, tool := range tools {
			handler := NewProxiedToolHandler(tm.sm, tm, tool.Name)
			serverTools = append(serverTools, server.ServerTool{
				Tool:    tool,
				Handler: handler.Handle,
			})
		}

		if err := tm.server.AddSessionTools(sessionID, serverTools...); err != nil {
			tm.metrics.recordRegisterFailure(ctx, capabilityTools, scopeSession)
			logger.WarnContext(ctx, "failed to add session tools", "session", sessionID, "error", err)
		} else {
			tm.metrics.recordRegistered(ctx, capabilityTools, scopeSession, len(tools))
			logger.InfoContext(ctx, "registered proxied tools", "session", sessionID, "tools", len(tools))
		}
	}

	// Resources and resource templates are registered the same way, referencing
	// the shared namespaced values.
	// One handler serves both: it recovers the datasource and upstream URI from
	// the namespaced URN of whatever URI the client sends back, whether that came
	// from a static resource or from expanding a template.
	resourceHandler := NewProxiedResourceHandler(tm.sm, tm)

	if len(resources) > 0 {
		serverResources := make([]server.ServerResource, 0, len(resources))
		for _, res := range resources {
			serverResources = append(serverResources, server.ServerResource{
				Resource: res,
				Handler:  resourceHandler.Handle,
			})
		}

		if err := tm.server.AddSessionResources(sessionID, serverResources...); err != nil {
			tm.metrics.recordRegisterFailure(ctx, capabilityResources, scopeSession)
			logger.WarnContext(ctx, "failed to add session resources", "session", sessionID, "error", err)
		} else {
			tm.metrics.recordRegistered(ctx, capabilityResources, scopeSession, len(resources))
			logger.InfoContext(ctx, "registered proxied resources", "session", sessionID, "resources", len(resources))
		}
	}

	if len(resourceTemplates) > 0 {
		serverTemplates := make([]server.ServerResourceTemplate, 0, len(resourceTemplates))
		for _, tmpl := range resourceTemplates {
			serverTemplates = append(serverTemplates, server.ServerResourceTemplate{
				Template: tmpl,
				Handler:  resourceHandler.Handle,
			})
		}

		if err := tm.server.AddSessionResourceTemplates(sessionID, serverTemplates...); err != nil {
			tm.metrics.recordRegisterFailure(ctx, capabilityResourceTemplates, scopeSession)
			logger.WarnContext(ctx, "failed to add session resource templates", "session", sessionID, "error", err)
		} else {
			tm.metrics.recordRegistered(ctx, capabilityResourceTemplates, scopeSession, len(resourceTemplates))
			logger.InfoContext(ctx, "registered proxied resource templates", "session", sessionID, "resource_templates", len(resourceTemplates))
		}
	}

	if len(prompts) > 0 {
		tm.metrics.recordSkipped(ctx, capabilityPrompts, skipReasonNoSessionPrompts, len(prompts))
		logger.WarnContext(ctx, "upstream MCP servers exposed prompts but this transport cannot scope prompts to a session; ignoring them",
			"session", sessionID, "prompts", len(prompts))
	}

	// The attach succeeded (usable set, ref held). Mark the session registered so
	// later hooks are no-ops; teardown will release the reference.
	state.proxiedRegistered = true
}

// releaseSessionProxiedToolSet releases the session's reference to its shared
// proxied tool set, if any. It is safe to call on sessions that never attached
// (no-op) and is guarded so a session releases its reference at most once. It
// releases by the set pointer the session holds, so it balances the reference
// even when the set was a failed build already removed from the cache.
func (tm *ToolManager) releaseSessionProxiedToolSet(state *SessionState) {
	state.mutex.Lock()
	set := state.proxiedSet
	if set == nil || state.proxiedSetReleased {
		state.mutex.Unlock()
		return
	}
	state.proxiedSetReleased = true
	state.proxiedSet = nil
	state.mutex.Unlock()

	tm.releaseProxiedToolSet(set)
}

// GetServerClient retrieves a proxied client from server-level storage (for stdio transport)
func (tm *ToolManager) GetServerClient(datasourceType, datasourceUID string) (*ProxiedClient, error) {
	tm.clientsMutex.RLock()
	defer tm.clientsMutex.RUnlock()

	key := datasourceType + "_" + datasourceUID
	client, exists := tm.serverClients[key]
	if !exists {
		// List available datasources to help with debugging
		var availableUIDs []string
		for _, c := range tm.serverClients {
			if c.DatasourceType == datasourceType {
				availableUIDs = append(availableUIDs, c.DatasourceUID)
			}
		}

		if len(availableUIDs) > 0 {
			return nil, fmt.Errorf("datasource '%s' not found. Available %s datasources: %v", datasourceUID, datasourceType, availableUIDs)
		}
		return nil, fmt.Errorf("datasource '%s' not found. No %s datasources with MCP support are configured", datasourceUID, datasourceType)
	}

	return client, nil
}
