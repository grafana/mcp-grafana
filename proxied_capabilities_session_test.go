package mcpgrafana

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// capableSession implements every per-session extension mcp-go needs for
// proxied capabilities: tools, resources, and resource templates. The real SSE
// and streamable-http sessions implement all three, so this is what the session
// registration path actually runs against in production.
type capableSession struct {
	*toolsCapableSession
	resources map[string]server.ServerResource
	templates map[string]server.ServerResourceTemplate
}

func newCapableSession(id string) *capableSession {
	return &capableSession{
		toolsCapableSession: newToolsCapableSession(id),
		resources:           map[string]server.ServerResource{},
		templates:           map[string]server.ServerResourceTemplate{},
	}
}

var (
	_ server.SessionWithTools             = (*capableSession)(nil)
	_ server.SessionWithResources         = (*capableSession)(nil)
	_ server.SessionWithResourceTemplates = (*capableSession)(nil)
)

func (s *capableSession) GetSessionResources() map[string]server.ServerResource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]server.ServerResource, len(s.resources))
	for k, v := range s.resources {
		out[k] = v
	}
	return out
}

func (s *capableSession) SetSessionResources(resources map[string]server.ServerResource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources = resources
}

func (s *capableSession) GetSessionResourceTemplates() map[string]server.ServerResourceTemplate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]server.ServerResourceTemplate, len(s.templates))
	for k, v := range s.templates {
		out[k] = v
	}
	return out
}

func (s *capableSession) SetSessionResourceTemplates(templates map[string]server.ServerResourceTemplate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templates = templates
}

// upstreamBuild returns a build result whose single client is the in-process
// fake upstream, with its capabilities namespaced exactly as buildProxiedToolSet
// would. Using the real namespacing helpers (rather than hand-written values)
// keeps these tests honest about what a client actually sees.
func upstreamBuild(t *testing.T, pc *ProxiedClient) builtProxiedTools {
	t.Helper()

	built := builtProxiedTools{
		clients:           map[string]*ProxiedClient{pc.DatasourceType + "_" + pc.DatasourceUID: pc},
		toolToDatasources: map[string][]string{},
	}
	for _, tool := range pc.ListTools() {
		built.tools = append(built.tools, addDatasourceUidParameter(tool, pc.DatasourceType))
	}
	for _, res := range pc.ListResources() {
		built.resources = append(built.resources, namespaceResource(res, pc))
	}
	for _, tmpl := range pc.ListResourceTemplates() {
		ns, ok := namespaceResourceTemplate(tmpl, pc)
		require.True(t, ok)
		built.resourceTemplates = append(built.resourceTemplates, ns)
	}
	for _, prompt := range pc.ListPrompts() {
		built.prompts = append(built.prompts, namespacePrompt(prompt, pc))
	}
	return built
}

// TestProxiedCapabilitiesRegisteredOnSession covers the per-session (HTTP/SSE)
// registration of the non-tool capabilities: resources and resource templates
// must land on the session under their namespaced identifiers, and prompts must
// be dropped with a warning rather than registered server-wide, where they would
// be visible to every other tenant's session.
func TestProxiedCapabilitiesRegisteredOnSession(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	var logs bytes.Buffer
	sm := NewSessionManager(WithSessionTTL(0))
	t.Cleanup(sm.Close)
	srv := server.NewMCPServer("test", "1.0")
	tm := NewToolManager(sm, srv,
		WithProxiedTools(true),
		WithToolManagerLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))),
	)
	tm.buildSet = func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		return upstreamBuild(t, pc), nil
	}
	sm.SetToolManager(tm)

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := newCapableSession("caps-1")
	sm.CreateSession(ctx, sess)
	require.NoError(t, srv.RegisterSession(ctx, sess))

	tm.InitializeAndRegisterProxiedCapabilities(ctx, sess)

	// Tools keep working exactly as before.
	require.Len(t, sess.GetSessionTools(), 1)
	_, hasTool := sess.GetSessionTools()["tempo_echo"]
	assert.True(t, hasTool, "the proxied tool must be registered under its namespaced name")

	// Resources are keyed by the namespaced URN, not the upstream URI: two Tempo
	// datasources exposing docs://traceql/basic must not collide.
	resources := sess.GetSessionResources()
	require.Len(t, resources, 1)
	wantURI := namespaceResourceURI("tempo", "abc-123", "docs://traceql/basic")
	res, ok := resources[wantURI]
	require.True(t, ok, "resource must be registered under %q, got %v", wantURI, resources)
	assert.Equal(t, "TraceQL Basics (Prod Tempo)", res.Resource.Name,
		"the datasource name disambiguates identical resources from several datasources")

	// Resource templates embed the upstream template verbatim inside the URN, so
	// a client's substitution still yields a namespaced URI we can route.
	templates := sess.GetSessionResourceTemplates()
	require.Len(t, templates, 1)
	for _, tmpl := range templates {
		assert.Equal(t, "urn:mcp-grafana:tempo:abc-123:docs://traceql/{section}", tmpl.Template.URITemplate.Raw())
		assert.Equal(t, "TraceQL Section (Prod Tempo)", tmpl.Template.Name)
	}

	// Prompts must NOT be registered: mcp-go has no AddSessionPrompts, so a
	// server-wide registration would cross tenants. The drop has to be visible.
	promptsResp := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`))
	promptNames := promptNamesFromResponse(t, promptsResp)
	assert.Empty(t, promptNames, "proxied prompts must not leak onto the shared server")
	assert.Contains(t, logs.String(), "cannot scope prompts to a session",
		"dropping upstream prompts must be logged, not silent")
}

// TestProxiedResourceReadRoutesThroughSession is the end-to-end guard for the
// session read path: a resources/read for a namespaced URI (static or produced
// by expanding a proxied template) must be routed by mcp-go to our handler, and
// forwarded to the right upstream with the original URI restored.
func TestProxiedResourceReadRoutesThroughSession(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	sm := NewSessionManager(WithSessionTTL(0))
	t.Cleanup(sm.Close)
	srv := server.NewMCPServer("test", "1.0", server.WithResourceCapabilities(false, true))
	tm := NewToolManager(sm, srv, WithProxiedTools(true))
	tm.buildSet = func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		return upstreamBuild(t, pc), nil
	}
	sm.SetToolManager(tm)

	baseCtx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := newCapableSession("read-1")
	sm.CreateSession(baseCtx, sess)
	require.NoError(t, srv.RegisterSession(baseCtx, sess))
	tm.InitializeAndRegisterProxiedCapabilities(baseCtx, sess)

	// Requests arrive with the session in the context, exactly as the HTTP
	// transports deliver them.
	ctx := srv.WithContext(baseCtx, sess)

	t.Run("static resource", func(t *testing.T) {
		uri := namespaceResourceURI("tempo", "abc-123", "docs://traceql/basic")
		contents := readResource(t, srv, ctx, uri)
		require.Len(t, contents, 1)
		// The upstream echoes its own (un-namespaced) URI, proving the URN was
		// unwrapped before forwarding.
		assert.Equal(t, "docs://traceql/basic", contents[0].URI)
		assert.Contains(t, contents[0].Text, "basic docs")
	})

	t.Run("template-expanded resource", func(t *testing.T) {
		// What a client sends after expanding the advertised template itself.
		contents := readResource(t, srv, ctx, "urn:mcp-grafana:tempo:abc-123:docs://traceql/metrics")
		require.Len(t, contents, 1)
		assert.Equal(t, "docs://traceql/metrics", contents[0].URI)
		assert.Contains(t, contents[0].Text, "templated docs for docs://traceql/metrics")
	})

	t.Run("another datasource's URN matches nothing", func(t *testing.T) {
		// Namespacing is what scopes a session to its own datasources: because
		// the UID is part of both the registered URI and the registered template,
		// a URN naming a datasource this session never discovered matches no
		// entry and is refused by mcp-go before reaching our handler.
		uri := namespaceResourceURI("tempo", "does-not-exist", "docs://traceql/basic")
		resp := srv.HandleMessage(ctx, readResourceMessage(t, uri))
		errResp, ok := resp.(mcp.JSONRPCError)
		require.True(t, ok, "expected a JSON-RPC error, got %T", resp)
		assert.Contains(t, errResp.Error.Message, "resource not found")
	})

	t.Run("handler rejects a URN for an unavailable datasource", func(t *testing.T) {
		// The same case reaching the handler directly (as it would if a template
		// for one UID somehow matched another): the client lookup must fail with
		// the caller-facing message rather than forwarding anywhere.
		req := mcp.ReadResourceRequest{}
		req.Params.URI = namespaceResourceURI("tempo", "does-not-exist", "docs://traceql/basic")

		_, err := NewProxiedResourceHandler(sm, tm).Handle(ctx, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found or not accessible")
	})
}

// TestProxiedCapabilitiesListedForSession checks the advertised surface a client
// actually enumerates: resources/list and resources/templates/list must return
// the namespaced entries for this session.
func TestProxiedCapabilitiesListedForSession(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	sm := NewSessionManager(WithSessionTTL(0))
	t.Cleanup(sm.Close)
	srv := server.NewMCPServer("test", "1.0", server.WithResourceCapabilities(false, true))
	tm := NewToolManager(sm, srv, WithProxiedTools(true))
	tm.buildSet = func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		return upstreamBuild(t, pc), nil
	}
	sm.SetToolManager(tm)

	baseCtx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := newCapableSession("list-1")
	sm.CreateSession(baseCtx, sess)
	require.NoError(t, srv.RegisterSession(baseCtx, sess))
	tm.InitializeAndRegisterProxiedCapabilities(baseCtx, sess)

	ctx := srv.WithContext(baseCtx, sess)

	var listed struct {
		Resources []mcp.Resource `json:"resources"`
	}
	unmarshalResult(t, srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)), &listed)
	require.Len(t, listed.Resources, 1)
	assert.Equal(t, namespaceResourceURI("tempo", "abc-123", "docs://traceql/basic"), listed.Resources[0].URI)

	var listedTemplates struct {
		ResourceTemplates []struct {
			URITemplate string `json:"uriTemplate"`
		} `json:"resourceTemplates"`
	}
	unmarshalResult(t, srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"resources/templates/list"}`)), &listedTemplates)
	require.Len(t, listedTemplates.ResourceTemplates, 1)
	assert.Equal(t, "urn:mcp-grafana:tempo:abc-123:docs://traceql/{section}", listedTemplates.ResourceTemplates[0].URITemplate)
}

// readResourceMessage builds a resources/read JSON-RPC request for uri.
func readResourceMessage(t *testing.T, uri string) []byte {
	t.Helper()
	msg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params":  map[string]any{"uri": uri},
	})
	require.NoError(t, err)
	return msg
}

// readResource issues a resources/read through the server and returns the text
// contents, failing the test on any JSON-RPC error.
func readResource(t *testing.T, srv *server.MCPServer, ctx context.Context, uri string) []mcp.TextResourceContents {
	t.Helper()

	var result struct {
		Contents []mcp.TextResourceContents `json:"contents"`
	}
	unmarshalResult(t, srv.HandleMessage(ctx, readResourceMessage(t, uri)), &result)
	return result.Contents
}

// unmarshalResult decodes a successful JSON-RPC response's result into out,
// failing the test if the server returned an error instead.
func unmarshalResult(t *testing.T, response mcp.JSONRPCMessage, out any) {
	t.Helper()

	raw, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(raw)), `"error"`, "unexpected JSON-RPC error: %s", raw)

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.NoError(t, json.Unmarshal(envelope.Result, out))
}

// promptNamesFromResponse extracts the prompt names from a prompts/list
// response, tolerating the error mcp-go returns when no prompt capability is
// registered at all (which is itself proof that no prompts were registered).
func promptNamesFromResponse(t *testing.T, response mcp.JSONRPCMessage) []string {
	t.Helper()

	raw, err := json.Marshal(response)
	require.NoError(t, err)
	var envelope struct {
		Result struct {
			Prompts []struct {
				Name string `json:"name"`
			} `json:"prompts"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))

	names := make([]string, 0, len(envelope.Result.Prompts))
	for _, p := range envelope.Result.Prompts {
		names = append(names, p.Name)
	}
	return names
}

// TestProxiedRequestSpanAttributes covers the tracing side: a proxied
// resources/read or prompts/get must record which datasource instance and
// upstream target it was routed to. Those are the only facts the generic
// observability hooks cannot recover, because the namespaced URN is opaque to
// them, and they are what makes a failing proxied request diagnosable.
func TestProxiedRequestSpanAttributes(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	sm := NewSessionManager(WithSessionTTL(0))
	t.Cleanup(sm.Close)
	srv := server.NewMCPServer("test", "1.0")
	tm := NewToolManager(sm, srv, WithProxiedTools(true))
	tm.serverMode = true
	tm.serverClients["tempo_abc-123"] = pc

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	spanAttrs := func(t *testing.T, run func(ctx context.Context)) map[string]string {
		t.Helper()
		ctx, span := tp.Tracer("test").Start(context.Background(), "request")
		run(ctx)
		span.End()

		var attrs map[string]string
		for _, s := range recorder.Ended() {
			if s.Name() != "request" {
				continue
			}
			attrs = map[string]string{}
			for _, kv := range s.Attributes() {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
		}
		require.NotNil(t, attrs, "the test span should have been recorded")
		return attrs
	}

	t.Run("resources/read", func(t *testing.T) {
		attrs := spanAttrs(t, func(ctx context.Context) {
			req := mcp.ReadResourceRequest{}
			req.Params.URI = namespaceResourceURI("tempo", "abc-123", "docs://traceql/basic")
			_, err := NewProxiedResourceHandler(sm, tm).Handle(ctx, req)
			require.NoError(t, err)
		})

		assert.Equal(t, "tempo", attrs["datasource.type"])
		assert.Equal(t, "abc-123", attrs["datasource.uid"])
		// The upstream URI, not the namespaced URN the client sent.
		assert.Equal(t, "docs://traceql/basic", attrs["mcp.proxied.upstream.uri"])
	})

	t.Run("prompts/get", func(t *testing.T) {
		attrs := spanAttrs(t, func(ctx context.Context) {
			req := mcp.GetPromptRequest{}
			req.Params.Name = namespacePromptName("tempo", "trace-summary")
			req.Params.Arguments = map[string]string{"datasourceUid": "abc-123", "trace_id": "abc"}
			_, err := NewProxiedPromptHandler(sm, tm).Handle(ctx, req)
			require.NoError(t, err)
		})

		assert.Equal(t, "tempo", attrs["datasource.type"])
		assert.Equal(t, "abc-123", attrs["datasource.uid"])
		assert.Equal(t, "trace-summary", attrs["mcp.proxied.upstream.prompt"])
	})
}

// TestProxiedCapabilityMetrics covers the metrics side of the proxied surface:
// what was re-exposed (by capability and scope) and what was dropped (by
// reason). Without the skipped counter, upstream prompts silently vanishing on
// an HTTP transport would be invisible to an operator.
func TestProxiedCapabilityMetrics(t *testing.T) {
	pc := newFakeUpstreamClient(t, "abc-123", "Prod Tempo", "tempo")

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	sm := NewSessionManager(WithSessionTTL(0))
	t.Cleanup(sm.Close)
	srv := server.NewMCPServer("test", "1.0")
	tm := NewToolManager(sm, srv,
		WithProxiedTools(true),
		WithToolManagerMeterProvider(mp),
	)
	tm.buildSet = func(ctx context.Context, logger *slog.Logger) (builtProxiedTools, error) {
		return upstreamBuild(t, pc), nil
	}
	sm.SetToolManager(tm)

	ctx := ctxWithCreds("http://grafana", "secret", nil, 1)
	sess := newCapableSession("metrics-1")
	sm.CreateSession(ctx, sess)
	require.NoError(t, srv.RegisterSession(ctx, sess))

	tm.InitializeAndRegisterProxiedCapabilities(ctx, sess)

	counts := collectCounters(t, reader)

	assert.Equal(t, int64(1), counts["mcp.proxied.capabilities_registered|tools|session"])
	assert.Equal(t, int64(1), counts["mcp.proxied.capabilities_registered|resources|session"])
	assert.Equal(t, int64(1), counts["mcp.proxied.capabilities_registered|resource_templates|session"])
	// Prompts are not registered on a per-session transport; they are counted as
	// skipped with the reason, not silently dropped.
	assert.Equal(t, int64(0), counts["mcp.proxied.capabilities_registered|prompts|session"])
	assert.Equal(t, int64(1), counts["mcp.proxied.capabilities_skipped|prompts|no_session_prompt_support"])
	assert.Equal(t, int64(0), counts["mcp.proxied.register_failure|tools|session"])
}

// collectCounters reads every Int64 sum from the reader and keys it by
// "<metric>|<capability>|<scope-or-reason>", so a test can assert one series
// without reconstructing the whole OTel data model each time. Missing series
// read as 0.
func collectCounters(t *testing.T, reader sdkmetric.Reader) map[string]int64 {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				capability, _ := dp.Attributes.Value(attrProxiedCapability)
				qualifier, ok := dp.Attributes.Value(attrProxiedScope)
				if !ok {
					qualifier, _ = dp.Attributes.Value(attrProxiedSkipReason)
				}
				out[m.Name+"|"+capability.Emit()+"|"+qualifier.Emit()] += dp.Value
			}
		}
	}
	return out
}
