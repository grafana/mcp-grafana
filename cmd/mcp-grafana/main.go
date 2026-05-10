package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/auth"
	"github.com/grafana/mcp-grafana/auth/rbac"
	"github.com/grafana/mcp-grafana/observability"
	"github.com/grafana/mcp-grafana/tools"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/semconv/v1.40.0/mcpconv"
)

func maybeAddTools(s *server.MCPServer, tf func(*server.MCPServer), enabledTools []string, disable bool, category string) {
	if !slices.Contains(enabledTools, category) {
		slog.Debug("Not enabling tools", "category", category)
		return
	}
	if disable {
		slog.Info("Disabling tools", "category", category)
		return
	}
	slog.Debug("Enabling tools", "category", category)
	tf(s)
}

// isCategoryEnabled reports whether a tool category is active given the
// enabled-tools list and the per-category disable flag.
func isCategoryEnabled(enabledTools []string, disabled bool, category string) bool {
	return slices.Contains(enabledTools, category) && !disabled
}

// categoryDescription maps a tool category to the description shown in server instructions.
var categoryDescription = map[string]string{
	"search":        "Search: Find dashboards, folders, and other Grafana resources.",
	"datasource":    "Datasources: List and fetch details for datasources.",
	"incident":      "Incidents: Search, create, update, and resolve incidents in Grafana Incident.",
	"prometheus":    "Prometheus: Run PromQL queries, retrieve metric metadata, and explore label names/values.",
	"loki":          "Loki: Run LogQL queries, retrieve log metadata, and explore label names/values.",
	"elasticsearch": "Elasticsearch and OpenSearch: Query Elasticsearch and OpenSearch datasources using Lucene syntax or Query DSL for logs and metrics.",
	"influxdb":      "InfluxDB: Query InfluxDB datasources.",
	"alerting":      "Alerting: List and fetch alert rules and notification contact points.",
	"dashboard":     "Dashboards: Search, retrieve, update, and create dashboards. Extract panel queries and datasource information.",
	"folder":        "Folders: Manage dashboard folders.",
	"oncall":        "OnCall: View and manage on-call schedules, shifts, teams, and users.",
	"asserts":       "Asserts: Query and analyze assertion data.",
	"sift":          "Sift Investigations: Start and manage Sift investigations, analyze logs/traces, find error patterns, and detect slow requests.",
	"admin":         "Admin: List teams and perform administrative tasks.",
	"pyroscope":     "Pyroscope: Profile applications and fetch profiling data.",
	"navigation":    "Navigation: Generate deeplink URLs for Grafana resources like dashboards, panels, and Explore queries.",
	"annotations":   "Annotations: Create and manage dashboard annotations.",
	"rendering":     "Rendering: Export dashboard panels or full dashboards as PNG images (requires Grafana Image Renderer plugin).",
	"plugin":        "Plugins: Check whether Grafana plugins are installed and fetch plugin details.",
	"cloudwatch":    "CloudWatch: Query AWS CloudWatch datasources for metrics and logs.",
	"examples":      "Examples: Query example tools.",
	"clickhouse":    "ClickHouse: Query ClickHouse datasources via Grafana with macro and variable substitution support.",
	"snowflake":     "Snowflake: Query Snowflake datasources via Grafana (including the SNOWFLAKE.TELEMETRY.EVENTS event table) with macro and variable substitution support.",
	"runpanelquery": "Run Panel Query: Execute panel queries directly.",
	"graphite":      "Graphite: Query Graphite datasources for metrics.",
	"api":           "API: Make authenticated HTTP requests to any Grafana API endpoint with optional jq-style response filtering.",
}

// disabledTools indicates whether each category of tools should be disabled.
type disabledTools struct {
	enabledTools string

	search, datasource, incident,
	prometheus, loki, elasticsearch, influxdb, alerting,
	dashboard, folder, oncall, asserts, sift, admin,
	pyroscope, navigation, proxied, annotations, rendering, cloudwatch, write,
	examples, clickhouse, snowflake, graphite,
	runpanelquery, plugin, api bool
}

// Configuration for the Grafana client.
type grafanaConfig struct {
	// Whether to enable debug mode for the Grafana transport.
	debug bool

	// TLS configuration
	tlsCertFile   string
	tlsKeyFile    string
	tlsCAFile     string
	tlsSkipVerify bool

	// Loki configuration
	maxLokiLogLimit int
}

func (dt *disabledTools) addFlags() {
	flag.StringVar(&dt.enabledTools, "enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering,plugin,api", "A comma separated list of tools enabled for this server. Can be overwritten entirely or by disabling specific components, e.g. --disable-search.")
	flag.BoolVar(&dt.search, "disable-search", false, "Disable search tools")
	flag.BoolVar(&dt.datasource, "disable-datasource", false, "Disable datasource tools")
	flag.BoolVar(&dt.incident, "disable-incident", false, "Disable incident tools")
	flag.BoolVar(&dt.prometheus, "disable-prometheus", false, "Disable prometheus tools")
	flag.BoolVar(&dt.loki, "disable-loki", false, "Disable loki tools")
	flag.BoolVar(&dt.elasticsearch, "disable-elasticsearch", false, "Disable elasticsearch and opensearch tools")
	flag.BoolVar(&dt.influxdb, "disable-influxdb", false, "Disable InfluxDB tools")
	flag.BoolVar(&dt.alerting, "disable-alerting", false, "Disable alerting tools")
	flag.BoolVar(&dt.dashboard, "disable-dashboard", false, "Disable dashboard tools")
	flag.BoolVar(&dt.folder, "disable-folder", false, "Disable folder tools")
	flag.BoolVar(&dt.oncall, "disable-oncall", false, "Disable oncall tools")
	flag.BoolVar(&dt.asserts, "disable-asserts", false, "Disable asserts tools")
	flag.BoolVar(&dt.sift, "disable-sift", false, "Disable sift tools")
	flag.BoolVar(&dt.admin, "disable-admin", false, "Disable admin tools")
	flag.BoolVar(&dt.pyroscope, "disable-pyroscope", false, "Disable pyroscope tools")
	flag.BoolVar(&dt.navigation, "disable-navigation", false, "Disable navigation tools")
	flag.BoolVar(&dt.proxied, "disable-proxied", false, "Disable proxied tools (tools from external MCP servers)")
	flag.BoolVar(&dt.write, "disable-write", false, "Disable write tools (create/update operations)")
	flag.BoolVar(&dt.annotations, "disable-annotations", false, "Disable annotation tools")
	flag.BoolVar(&dt.rendering, "disable-rendering", false, "Disable rendering tools (panel/dashboard image export)")
	flag.BoolVar(&dt.cloudwatch, "disable-cloudwatch", false, "Disable CloudWatch tools")
	flag.BoolVar(&dt.examples, "disable-examples", false, "Disable query examples tools")
	flag.BoolVar(&dt.clickhouse, "disable-clickhouse", false, "Disable ClickHouse tools")
	flag.BoolVar(&dt.snowflake, "disable-snowflake", false, "Disable Snowflake tools")
	flag.BoolVar(&dt.runpanelquery, "disable-runpanelquery", false, "Disable run panel query tools")
	flag.BoolVar(&dt.graphite, "disable-graphite", false, "Disable Graphite tools")
	flag.BoolVar(&dt.plugin, "disable-plugin", false, "Disable plugin tools")
	flag.BoolVar(&dt.api, "disable-api", false, "Disable API tools")
}

func (gc *grafanaConfig) addFlags() {
	flag.BoolVar(&gc.debug, "debug", false, "Enable debug mode for the Grafana transport")

	// TLS configuration flags
	flag.StringVar(&gc.tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file for client authentication")
	flag.StringVar(&gc.tlsKeyFile, "tls-key-file", "", "Path to TLS private key file for client authentication")
	flag.StringVar(&gc.tlsCAFile, "tls-ca-file", "", "Path to TLS CA certificate file for server verification")
	flag.BoolVar(&gc.tlsSkipVerify, "tls-skip-verify", false, "Skip TLS certificate verification (insecure)")

	// Loki configuration flags
	flag.IntVar(&gc.maxLokiLogLimit, "max-loki-log-limit", tools.MaxLokiLogLimit, "Maximum number of log lines returned per query_loki_logs call")
}

// toolEntry pairs a tool registration function with its category and disable flag.
type toolEntry struct {
	adder    func(*server.MCPServer)
	disabled bool
	category string
}

// toolEntries returns the ordered list of tool categories with their registration
// functions. This is the single source of truth for category-to-adder mapping,
// used by both processTools (registration) and buildInstructions (instructions).
func (dt *disabledTools) toolEntries() []toolEntry {
	enableWriteTools := !dt.write
	return []toolEntry{
		{tools.AddSearchTools, dt.search, "search"},
		{tools.AddDatasourceTools, dt.datasource, "datasource"},
		{func(mcp *server.MCPServer) { tools.AddIncidentTools(mcp, enableWriteTools) }, dt.incident, "incident"},
		{tools.AddPrometheusTools, dt.prometheus, "prometheus"},
		{tools.AddLokiTools, dt.loki, "loki"},
		{tools.AddElasticsearchTools, dt.elasticsearch, "elasticsearch"},
		{tools.AddInfluxDBTools, dt.influxdb, "influxdb"},
		{func(mcp *server.MCPServer) { tools.AddAlertingTools(mcp, enableWriteTools) }, dt.alerting, "alerting"},
		{func(mcp *server.MCPServer) { tools.AddDashboardTools(mcp, enableWriteTools) }, dt.dashboard, "dashboard"},
		{func(mcp *server.MCPServer) { tools.AddFolderTools(mcp, enableWriteTools) }, dt.folder, "folder"},
		{tools.AddOnCallTools, dt.oncall, "oncall"},
		{tools.AddAssertsTools, dt.asserts, "asserts"},
		{func(mcp *server.MCPServer) { tools.AddSiftTools(mcp, enableWriteTools) }, dt.sift, "sift"},
		{tools.AddAdminTools, dt.admin, "admin"},
		{tools.AddPyroscopeTools, dt.pyroscope, "pyroscope"},
		{tools.AddNavigationTools, dt.navigation, "navigation"},
		{func(mcp *server.MCPServer) { tools.AddAnnotationTools(mcp, enableWriteTools) }, dt.annotations, "annotations"},
		{tools.AddRenderingTools, dt.rendering, "rendering"},
		{tools.AddCloudWatchTools, dt.cloudwatch, "cloudwatch"},
		{tools.AddExamplesTools, dt.examples, "examples"},
		{tools.AddClickHouseTools, dt.clickhouse, "clickhouse"},
		{tools.AddSnowflakeTools, dt.snowflake, "snowflake"},
		{tools.AddRunPanelQueryTools, dt.runpanelquery, "runpanelquery"},
		{tools.AddGraphiteTools, dt.graphite, "graphite"},
		{func(mcp *server.MCPServer) { tools.AddPluginTools(mcp, enableWriteTools) }, dt.plugin, "plugin"},
		{func(mcp *server.MCPServer) { tools.AddAPITools(mcp, enableWriteTools) }, dt.api, "api"},
	}
}

// processTools registers enabled tool categories on the server.
func (dt *disabledTools) processTools(s *server.MCPServer) {
	enabledTools := strings.Split(dt.enabledTools, ",")
	for _, e := range dt.toolEntries() {
		maybeAddTools(s, e.adder, enabledTools, e.disabled, e.category)
	}
}

// buildInstructions constructs the server instruction string listing only
// the capabilities that are actually enabled.
func (dt *disabledTools) buildInstructions() string {
	enabledTools := strings.Split(dt.enabledTools, ",")

	var capabilities []string
	for _, e := range dt.toolEntries() {
		if !isCategoryEnabled(enabledTools, e.disabled, e.category) {
			continue
		}
		if desc, ok := categoryDescription[e.category]; ok {
			capabilities = append(capabilities, desc)
		}
	}

	// Proxied tools are registered via hooks (not maybeAddTools), so they
	// are not in toolEntries. Include their description when enabled.
	if !dt.proxied {
		capabilities = append(capabilities, "Proxied Tools: Access tools from external MCP servers (like Tempo) through dynamic discovery.")
	}

	var b strings.Builder
	b.WriteString("This server provides access to your Grafana instance and the surrounding ecosystem.\n\n")

	if len(capabilities) > 0 {
		b.WriteString("Available Capabilities:\n")
		for _, c := range capabilities {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("No tool categories are currently enabled.\n")
	}

	b.WriteString("\nTimestamp parameters without a timezone offset are interpreted as UTC. Include an offset like '-05:00' or use relative syntax like 'now-1h' to query in a different timezone.\n")
	return b.String()
}

func newServer(transport string, dt disabledTools, obs *observability.Observability, sessionIdleTimeoutMinutes int, rbacEngine *rbac.Engine) (*server.MCPServer, *mcpgrafana.ToolManager, *mcpgrafana.SessionManager) {
	sm := mcpgrafana.NewSessionManager(
		mcpgrafana.WithSessionTTL(time.Duration(sessionIdleTimeoutMinutes) * time.Minute),
	)

	// Declare variables that will be initialized after server creation.
	// The hooks below capture these by pointer, so they must be declared first.
	var stm *mcpgrafana.ToolManager
	var s *server.MCPServer

	// Create hooks
	hooks := &server.Hooks{
		OnRegisterSession:   []server.OnRegisterSessionHookFunc{sm.CreateSession},
		OnUnregisterSession: []server.OnUnregisterSessionHookFunc{sm.RemoveSession},
	}

	// Add proxied tools hooks if enabled and we're not running in stdio mode.
	// (stdio mode is handled by InitializeAndRegisterServerTools; per-session tools
	// are not supported).
	if transport != "stdio" && !dt.proxied {
		// ensureSessionRegistered registers an ephemeral session in MCPServer.sessions
		// if it's not already there. This is needed for horizontal scaling: when a
		// request lands on a pod that didn't handle the initialize call, the SDK
		// creates an ephemeral session that isn't registered, causing AddSessionTools
		// to fail with ErrSessionNotFound. RegisterSession uses LoadOrStore
		// internally, so this is a no-op for already-registered sessions.
		ensureSessionRegistered := func(ctx context.Context) {
			if s != nil {
				if session := server.ClientSessionFromContext(ctx); session != nil {
					_ = s.RegisterSession(ctx, session)
				}
			}
		}

		// OnBeforeListTools: Discover, connect, and register tools
		hooks.OnBeforeListTools = []server.OnBeforeListToolsFunc{
			func(ctx context.Context, id any, request *mcp.ListToolsRequest) {
				ensureSessionRegistered(ctx)
				if stm != nil {
					if session := server.ClientSessionFromContext(ctx); session != nil {
						stm.InitializeAndRegisterProxiedTools(ctx, session)
					}
				}
			},
		}

		// OnBeforeCallTool: Fallback in case client calls tool without listing first
		hooks.OnBeforeCallTool = []server.OnBeforeCallToolFunc{
			func(ctx context.Context, id any, request *mcp.CallToolRequest) {
				ensureSessionRegistered(ctx)
				if stm != nil {
					if session := server.ClientSessionFromContext(ctx); session != nil {
						stm.InitializeAndRegisterProxiedTools(ctx, session)
					}
				}
			},
		}
	}

	// Merge observability hooks with existing hooks
	hooks = observability.MergeHooks(hooks, obs.MCPHooks())

	if rbacEngine != nil {
		hooks.OnAfterListTools = append(hooks.OnAfterListTools, rbacEngine.HookOnAfterListTools())
	}

	// Register tools and build the instruction string from enabled categories.
	// processTools both registers tools on the server and collects descriptions
	// of enabled categories, so we need a temporary nil server reference first.
	// Instead, we split: compute instructions from flags, then create server,
	// then register tools.
	instructions := dt.buildInstructions()

	s = server.NewMCPServer("mcp-grafana", mcpgrafana.Version(),
		server.WithInstructions(instructions),
		server.WithHooks(hooks),
	)

	// Initialize ToolManager now that server is created
	stm = mcpgrafana.NewToolManager(sm, s, mcpgrafana.WithProxiedTools(!dt.proxied))

	// Give the SessionManager a reference to the MCPServer so the reaper can
	// unregister sessions from the SDK's internal session map.
	sm.SetMCPServer(s)

	dt.processTools(s)
	return s, stm, sm
}

type tlsConfig struct {
	certFile, keyFile string
}

func (tc *tlsConfig) addFlags() {
	flag.StringVar(&tc.certFile, "server.tls-cert-file", "", "Path to TLS certificate file for server HTTPS (required for TLS)")
	flag.StringVar(&tc.keyFile, "server.tls-key-file", "", "Path to TLS private key file for server HTTPS (required for TLS)")
}

// httpServer represents a server with Start and Shutdown methods
type httpServer interface {
	Start(addr string) error
	Shutdown(ctx context.Context) error
}

// runHTTPServer handles the common logic for running HTTP-based servers
func runHTTPServer(ctx context.Context, srv httpServer, addr, transportName string) error {
	// Start server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Start(addr); err != nil {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Wait for either server error or shutdown signal
	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		slog.Info(fmt.Sprintf("%s server shutting down...", transportName))

		// Create a timeout context for shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown error: %v", err)
		}
		slog.Debug("Shutdown called, waiting for connections to close...")

		// Wait for server to finish
		select {
		case err := <-serverErr:
			// http.ErrServerClosed is expected when shutting down
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("server error during shutdown: %v", err)
			}
		case <-shutdownCtx.Done():
			slog.Warn(fmt.Sprintf("%s server did not stop gracefully within timeout", transportName))
		}
	}

	return nil
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// runMetricsServer starts a separate HTTP server for metrics.
func runMetricsServer(addr string, o *observability.Observability) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", o.MetricsHandler())
	slog.Info("Starting metrics server", "address", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("metrics server error", "error", err)
	}
}

func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig, obs observability.Config, sessionIdleTimeoutMinutes int, authCfg auth.Config) error {
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(stderrHandler))

	o, err := observability.Setup(obs)
	if err != nil {
		return fmt.Errorf("failed to setup observability: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := o.Shutdown(shutdownCtx); err != nil {
			slog.Error("failed to shutdown observability", "error", err)
		}
	}()

	// The otelslog bridge attaches trace_id / span_id from context, so log
	// records correlate with the spans mcp-grafana already emits.
	if lp := o.LoggerProvider(); lp != nil {
		otlpHandler := otelslog.NewHandler("mcp-grafana", otelslog.WithLoggerProvider(lp))
		slog.SetDefault(slog.New(observability.NewFanoutHandler(stderrHandler, otlpHandler)))
		// Announce through the fanout so both stderr and OTLP subscribers see
		// the startup signal. If the first OTLP batch fails, the stderr branch
		// of the fanout still lands the record.
		slog.Info("OTLP log export configured", "endpoint", observability.OTLPLogsEndpoint())
	}

	// Create a client cache for HTTP-based transports to avoid per-request
	// transport allocation (see https://github.com/grafana/mcp-grafana/issues/682).
	var clientCache *mcpgrafana.ClientCache
	if transport != "stdio" {
		clientCache = mcpgrafana.NewClientCache(nil)
		defer clientCache.Close()
	}

	// Create a context that will be cancelled on shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		authSrv        *auth.Server
		authGrafanaURL string
	)
	if authCfg.Mode != auth.ModeNone {
		// stdio transport doesn't expose HTTP endpoints, so /authorize,
		// /callback, /token, etc. would never be reached. Skip
		// buildAuthServer entirely on stdio — calling it would do
		// network-bound work (e.g. OIDC discovery) only to throw the
		// result away, and would block startup if the issuer is
		// unreachable even though the resulting Server can't be used.
		if transport == "stdio" {
			slog.Warn("--auth-mode is ignored on stdio transport")
		} else {
			authGrafanaURL = os.Getenv("GRAFANA_URL")
			if authGrafanaURL == "" {
				authGrafanaURL = "http://localhost:3000"
			}
			var err error
			authSrv, err = buildAuthServer(ctx, authCfg, authGrafanaURL, slog.Default())
			if err != nil {
				return fmt.Errorf("auth setup: %w", err)
			}
			slog.Info("Per-user auth enabled", "mode", string(authCfg.Mode), "public_url", authCfg.PublicURL, "trust_forwarded_headers", authCfg.TrustForwardedHeaders)
		}
		rbacMode, err := rbac.ParseMode(authCfg.RBACGating)
		if err != nil {
			return fmt.Errorf("rbac: %w", err)
		}
		permsClient := rbac.NewPermsClient(authGrafanaURL, nil)
		rbacFetcher := func(ctx context.Context, sessionKey string) (rbac.Snapshot, error) {
			// Recover the per-user upstream credential by looking up the session in
			// the store and decrypting the stored ciphertext. We cannot read it from
			// GrafanaConfig because ComposedHTTPContextFunc resets the config from
			// environment/headers before the hook runs, discarding the value the auth
			// middleware placed there.
			sess, err := authSrv.Store.GetSessionByTokenHash(ctx, sessionKey)
			if err != nil {
				return rbac.Snapshot{}, rbac.ErrFetchFailed
			}
			pt, err := authSrv.Encryptor.Open(sess.UpstreamCredsCT)
			if err != nil {
				return rbac.Snapshot{}, rbac.ErrFetchFailed
			}
			saToken := string(pt)
			perms, err := permsClient.Fetch(ctx, saToken)
			if err != nil {
				return rbac.Snapshot{}, err
			}
			// Derive basic role from the user's permissions, if present. Grafana
			// returns the basic role as a separate header on /api/user; for now we
			// approximate from the "fixed:..." role permissions if present, else
			// leave empty (basic-mode tools without a basic role won't be visible).
			role := basicRoleFromPerms(perms)
			return rbac.Snapshot{
				Permissions: perms,
				BasicRole:   role,
				FetchedAt:   time.Now(),
			}, nil
		}
		ttl := authCfg.RBACCacheTTL
		if ttl == 0 {
			ttl = 5 * time.Minute
		}
		rbacCache := rbac.NewCache(ttl, rbacFetcher)
		authSrv.RBAC = rbac.NewEngine(rbac.EngineConfig{
			Mode:           rbacMode,
			Cache:          rbacCache,
			Gate:           rbac.NewGate(rbac.ToolGates),
			KeyFromContext: auth.SessionKeyFromContext,
			Metrics:        rbac.NewMetrics(),
			Logger:         slog.Default(),
		})
	}

	var rbacEngine *rbac.Engine
	if authSrv != nil {
		rbacEngine = authSrv.RBAC
	}
	s, tm, sm := newServer(transport, dt, o, sessionIdleTimeoutMinutes, rbacEngine)
	defer sm.Close()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Handle shutdown signals
	go func() {
		<-sigChan
		slog.Info("Received shutdown signal")
		cancel()

		// For stdio, close stdin to unblock the Listen call
		if transport == "stdio" {
			_ = os.Stdin.Close()
		}
	}()

	// Start the appropriate server based on transport
	switch transport {
	case "stdio":
		srv := server.NewStdioServer(s)
		cf := mcpgrafana.ComposedStdioContextFunc(gc)
		srv.SetContextFunc(cf)

		// For stdio (single-tenant), initialize proxied tools on the server directly
		if !dt.proxied {
			stdioCtx := cf(ctx)
			if err := tm.InitializeAndRegisterServerTools(stdioCtx); err != nil {
				slog.Error("failed to initialize proxied tools for stdio", "error", err)
			}
		}

		slog.Info("Starting Grafana MCP server using stdio transport", "version", mcpgrafana.Version())

		err := srv.Listen(ctx, os.Stdin, os.Stdout)
		if err != nil && err != context.Canceled {
			return fmt.Errorf("server error: %v", err)
		}
		return nil

	case "sse":
		httpSrv := &http.Server{Addr: addr}
		srv := server.NewSSEServer(s,
			server.WithSSEContextFunc(mcpgrafana.ComposedSSEContextFunc(gc, clientCache)),
			server.WithStaticBasePath(basePath),
			server.WithHTTPServer(httpSrv),
		)
		mux := http.NewServeMux()
		if basePath == "" {
			basePath = "/"
		}
		mcpHandler := http.Handler(mcpgrafana.ValidateGrafanaURLMiddleware(srv))
		if authSrv != nil {
			mcpHandler = authSrv.Middleware()(mcpHandler)
			authSrv.RegisterRoutes(mux, authGrafanaURL, authCfg.AllowInsecure)
		}
		mux.Handle(basePath, observability.WrapHandler(mcpHandler, basePath))
		mux.HandleFunc("/healthz", handleHealthz)
		if obs.MetricsEnabled {
			if obs.MetricsAddress == "" {
				mux.Handle("/metrics", o.MetricsHandler())
			} else {
				go runMetricsServer(obs.MetricsAddress, o)
			}
		}
		httpSrv.Handler = mux
		slog.Info("Starting Grafana MCP server using SSE transport",
			"version", mcpgrafana.Version(), "address", addr, "basePath", basePath, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, srv, addr, "SSE")
	case "streamable-http":
		httpSrv := &http.Server{Addr: addr}
		opts := []server.StreamableHTTPOption{
			server.WithHTTPContextFunc(mcpgrafana.ComposedHTTPContextFunc(gc, clientCache)),
			server.WithStateLess(dt.proxied), // Stateful when proxied tools enabled (requires sessions)
			server.WithEndpointPath(endpointPath),
			server.WithStreamableHTTPServer(httpSrv),
		}
		if tls.certFile != "" || tls.keyFile != "" {
			opts = append(opts, server.WithTLSCert(tls.certFile, tls.keyFile))
		}
		srv := server.NewStreamableHTTPServer(s, opts...)
		mux := http.NewServeMux()
		mcpHandler := http.Handler(mcpgrafana.ValidateGrafanaURLMiddleware(srv))
		if authSrv != nil {
			mcpHandler = authSrv.Middleware()(mcpHandler)
			authSrv.RegisterRoutes(mux, authGrafanaURL, authCfg.AllowInsecure)
		}
		mux.Handle(endpointPath, observability.WrapHandler(mcpHandler, endpointPath))
		mux.HandleFunc("/healthz", handleHealthz)
		if obs.MetricsEnabled {
			if obs.MetricsAddress == "" {
				mux.Handle("/metrics", o.MetricsHandler())
			} else {
				go runMetricsServer(obs.MetricsAddress, o)
			}
		}
		httpSrv.Handler = mux
		slog.Info("Starting Grafana MCP server using StreamableHTTP transport",
			"version", mcpgrafana.Version(), "address", addr, "endpointPath", endpointPath, "metrics", obs.MetricsEnabled)
		return runHTTPServer(ctx, srv, addr, "StreamableHTTP")
	default:
		return fmt.Errorf("invalid transport type: %s. Must be 'stdio', 'sse' or 'streamable-http'", transport)
	}
}

func buildAuthConfig(modeStr, publicURL, encKey, encKeyPrev, stateDir string, allowInsecure, trustForwarded bool, oidcIssuer, oidcClientID, oidcClientSecret, oidcScopes string, rbacGating string, rbacCacheTTL time.Duration) (auth.Config, error) {
	mode, err := auth.ParseMode(modeStr)
	if err != nil {
		return auth.Config{}, err
	}
	cfg := auth.Config{
		Mode:                  mode,
		PublicURL:             strings.TrimRight(publicURL, "/"),
		StateDir:              stateDir,
		AllowInsecure:         allowInsecure,
		TrustForwardedHeaders: trustForwarded,
		OIDCIssuerURL:         strings.TrimRight(oidcIssuer, "/"),
		OIDCClientID:          oidcClientID,
		OIDCClientSecret:      oidcClientSecret,
		OIDCScopes:            strings.Fields(oidcScopes),
	}
	cfg.RBACGating = strings.ToLower(strings.TrimSpace(rbacGating))
	cfg.RBACCacheTTL = rbacCacheTTL
	if encKey != "" {
		k, err := auth.DecodeKey(encKey)
		if err != nil {
			return cfg, fmt.Errorf("--token-encryption-key: %w", err)
		}
		cfg.EncryptionKey = k
	}
	if encKeyPrev != "" {
		k, err := auth.DecodeKey(encKeyPrev)
		if err != nil {
			return cfg, fmt.Errorf("--token-encryption-key-previous: %w", err)
		}
		cfg.EncryptionKeyPrevious = k
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func buildAuthServer(ctx context.Context, cfg auth.Config, grafanaURL string, logger *slog.Logger) (*auth.Server, error) {
	enc, err := auth.NewEncryptor(cfg.EncryptionKey, cfg.EncryptionKeyPrevious)
	if err != nil {
		return nil, fmt.Errorf("encryptor: %w", err)
	}

	var store auth.Store
	if cfg.StateDir != "" {
		path := filepath.Join(cfg.StateDir, "auth.state")
		fs, err := auth.NewFileStore(path, enc)
		if err != nil {
			return nil, fmt.Errorf("file store at %s: %w", path, err)
		}
		store = fs
	} else {
		store = auth.NewMemoryStore()
	}

	var up auth.Upstream
	switch cfg.Mode {
	case auth.ModeOAuthOIDC:
		oidc, err := auth.NewOIDCUpstream(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("oidc upstream: %w", err)
		}
		up = oidc
	default:
		return nil, fmt.Errorf("auth mode %q is not implemented in Phase 1", cfg.Mode)
	}

	if !strings.HasPrefix(grafanaURL, "http://") && !strings.HasPrefix(grafanaURL, "https://") {
		return nil, fmt.Errorf("GRAFANA_URL must be set when --auth-mode != none")
	}

	return auth.New(cfg, store, enc, up, logger), nil
}

func main() {
	var transport string
	flag.StringVar(&transport, "t", "stdio", "Transport type (stdio, sse or streamable-http)")
	flag.StringVar(
		&transport,
		"transport",
		"stdio",
		"Transport type (stdio, sse or streamable-http)",
	)
	addr := flag.String("address", "localhost:8000", "The host and port to start the sse server on")
	basePath := flag.String("base-path", "", "Base path for the sse server")
	endpointPath := flag.String("endpoint-path", "/mcp", "Endpoint path for the streamable-http server")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	sessionIdleTimeoutMinutes := flag.Int("session-idle-timeout-minutes", 30, "Session idle timeout in minutes. Sessions with no activity for this duration are automatically reaped. Set to 0 to disable session reaping")
	showVersion := flag.Bool("version", false, "Print the version and exit")
	var dt disabledTools
	dt.addFlags()
	var gc grafanaConfig
	gc.addFlags()
	var tls tlsConfig
	tls.addFlags()
	var obs observability.Config
	flag.BoolVar(&obs.MetricsEnabled, "metrics", false, "Enable Prometheus metrics endpoint")
	flag.StringVar(&obs.MetricsAddress, "metrics-address", "", "Separate address for metrics server (e.g., :9090). If empty, metrics are served on the main server at /metrics")
	flag.DurationVar(&obs.SlowRequestThreshold, "slow-request-threshold", 0, "Log an event when any MCP request (tool invocation, list, resource read, etc.) takes longer than this threshold. Accepts Go duration strings, e.g. 500ms, 5s. Default 0 disables slow-request logging.")
	var slowRequestLogLevelStr string
	flag.StringVar(&slowRequestLogLevelStr, "slow-request-log-level", "warn", "Log level for slow-request events. One of \"info\" or \"warn\". Default \"warn\".")
	var authModeStr, authPublicURL, authStateDir, authEncKey, authEncKeyPrev string
	var authAllowInsecure, authTrustForwarded bool
	var oidcIssuer, oidcClientID, oidcClientSecret, oidcScopes string
	flag.StringVar(&authModeStr, "auth-mode", "none", "Per-user auth mode: 'none' (default), 'oauth-oidc'")
	flag.StringVar(&authPublicURL, "public-url", "", "Public URL of this MCP server (required when --auth-mode != none)")
	flag.StringVar(&authEncKey, "token-encryption-key", "", "AES-GCM key for encrypting stored credentials (32 bytes, base64 or hex). Required when --auth-mode != none.")
	flag.StringVar(&authEncKeyPrev, "token-encryption-key-previous", "", "Previous AES-GCM key, accepted for decryption during rotation")
	flag.StringVar(&authStateDir, "auth-state-dir", "", "Directory for persistent auth state. Empty = in-memory store.")
	flag.BoolVar(&authAllowInsecure, "allow-insecure-auth", false, "Permit auth endpoints over plain HTTP (dev only).")
	flag.BoolVar(&authTrustForwarded, "trust-forwarded-headers", false, "Honour X-Forwarded-For / X-Real-IP / X-Forwarded-Proto from inbound requests. Set ONLY when a header-stripping reverse proxy fronts mcp-grafana — without one, attackers can spoof these per request to bypass per-IP rate limits and the auth-endpoint HTTPS guard.")
	flag.StringVar(&oidcIssuer, "oidc-issuer-url", "", "OIDC issuer URL (oauth-oidc mode)")
	flag.StringVar(&oidcClientID, "oidc-client-id", "", "OIDC client_id (oauth-oidc mode)")
	flag.StringVar(&oidcClientSecret, "oidc-client-secret", "", "OIDC client_secret (oauth-oidc mode)")
	flag.StringVar(&oidcScopes, "oidc-scopes", "openid profile email", "Space-separated OIDC scopes")
	var rbacGating string
	var rbacCacheTTL time.Duration
	flag.StringVar(&rbacGating, "rbac-gating", "auto", "RBAC tool gating mode: 'auto' (default), 'enterprise', 'basic', 'off'.")
	flag.DurationVar(&rbacCacheTTL, "rbac-cache-ttl", 5*time.Minute, "How long to cache a user's permission set before refetching from Grafana.")
	flag.Parse()

	action, slowLevel, err := handleFlagsPostParse(*showVersion, slowRequestLogLevelStr)
	switch action {
	case flagActionVersion:
		fmt.Println(mcpgrafana.Version())
		os.Exit(0)
	case flagActionInvalidSlowLevel:
		fmt.Fprintf(os.Stderr, "invalid --slow-request-log-level: %v\n", err)
		os.Exit(2)
	case flagActionContinue:
		obs.SlowRequestLogLevel = slowLevel
	default:
		// flagActionUnset or any unexpected value — refuse to proceed silently.
		fmt.Fprintf(os.Stderr, "internal error: unexpected flag action %v\n", action)
		os.Exit(2)
	}

	authCfg, err := buildAuthConfig(authModeStr, authPublicURL, authEncKey, authEncKeyPrev, authStateDir, authAllowInsecure, authTrustForwarded, oidcIssuer, oidcClientID, oidcClientSecret, oidcScopes, rbacGating, rbacCacheTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid auth config: %v\n", err)
		os.Exit(2)
	}
	// authCfg is consumed inside run()

	// Convert local grafanaConfig to mcpgrafana.GrafanaConfig
	grafanaConfig := mcpgrafana.GrafanaConfig{
		Debug:           gc.debug,
		MaxLokiLogLimit: gc.maxLokiLogLimit,
	}
	if gc.tlsCertFile != "" || gc.tlsKeyFile != "" || gc.tlsCAFile != "" || gc.tlsSkipVerify {
		grafanaConfig.TLSConfig = &mcpgrafana.TLSConfig{
			CertFile:   gc.tlsCertFile,
			KeyFile:    gc.tlsKeyFile,
			CAFile:     gc.tlsCAFile,
			SkipVerify: gc.tlsSkipVerify,
		}
	}

	// Set OTel resource identity
	obs.ServerName = "mcp-grafana"
	obs.ServerVersion = mcpgrafana.Version()

	// Map transport flag to semconv network.transport values
	switch transport {
	case "stdio":
		obs.NetworkTransport = mcpconv.NetworkTransportPipe
	case "sse", "streamable-http":
		obs.NetworkTransport = mcpconv.NetworkTransportTCP
	}

	if err := run(transport, *addr, *basePath, *endpointPath, parseLevel(*logLevel), dt, grafanaConfig, tls, obs, *sessionIdleTimeoutMinutes, authCfg); err != nil {
		panic(err)
	}
}

func parseLevel(level string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}
	return l
}

// parseSlowRequestLogLevel parses the --slow-request-log-level flag value.
// Only "info" and "warn" are accepted (case-insensitive). Any other value,
// including the empty string or values with surrounding whitespace, returns
// a non-nil error so main() can fail-fast on misconfiguration rather than
// silently defaulting.
//
// On error the returned slog.Level is the zero value (slog.LevelInfo == 0).
// Callers MUST check the error before using the level; using the zero level
// on a rejected input would silently select INFO, which is not the CLI's
// advertised default of WARN.
func parseSlowRequestLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	default:
		return 0, fmt.Errorf("must be \"info\" or \"warn\", got %q", s)
	}
}

// flagAction encodes what main() should do after flag.Parse().
// flagActionUnset is reserved as the zero value so an accidentally-zero-valued
// return from a future code path trips the switch's default: case rather
// than silently taking the Continue branch.
type flagAction int

const (
	flagActionUnset flagAction = iota
	flagActionContinue
	flagActionVersion
	flagActionInvalidSlowLevel
)

// handleFlagsPostParse decides what main() should do after flag.Parse().
// It is pure (no os.Exit, no I/O) so it is unit-testable. --version
// short-circuits before slow-request-log-level validation so it prints
// regardless of other flags' values (matches pre-#756 behavior).
//
// The returned slog.Level is only meaningful when action == flagActionContinue;
// the other branches return a zero level that the caller must not read.
func handleFlagsPostParse(showVersion bool, slowLevelStr string) (flagAction, slog.Level, error) {
	if showVersion {
		return flagActionVersion, 0, nil
	}
	slowLevel, err := parseSlowRequestLogLevel(slowLevelStr)
	if err != nil {
		return flagActionInvalidSlowLevel, 0, err
	}
	return flagActionContinue, slowLevel, nil
}

// basicRoleFromPerms approximates the user's BasicRole from their permission
// set. Grafana's RBAC engine includes role-membership permissions on the
// /user/permissions response; we look for the "fixed:" role names. This is
// best-effort; if no signal, returns empty (caller treats as no role).
func basicRoleFromPerms(perms rbac.PermissionSet) string {
	// Heuristic: if the user can manage roles, they're at least Admin.
	// If they can write to dashboards/datasources, they're at least Editor.
	// If they can read, they're at least Viewer.
	if _, ok := perms["roles:write"]; ok {
		return "Admin"
	}
	if _, ok := perms["users:write"]; ok {
		return "Admin"
	}
	if _, ok := perms["dashboards:write"]; ok {
		return "Editor"
	}
	if _, ok := perms["datasources:write"]; ok {
		return "Editor"
	}
	if _, ok := perms["dashboards:read"]; ok {
		return "Viewer"
	}
	return ""
}
