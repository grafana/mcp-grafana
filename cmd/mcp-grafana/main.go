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
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/observability"
	"github.com/grafana/mcp-grafana/tools"
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

// disabledTools indicates whether each category of tools should be disabled.
type disabledTools struct {
	enabledTools string

	search, datasource, incident,
	prometheus, loki, elasticsearch, influxdb, alerting,
	dashboard, folder, oncall, asserts, sift, admin,
	pyroscope, navigation, proxied, annotations, rendering, cloudwatch, write,
	examples, clickhouse, graphite,
	runpanelquery bool
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
	flag.StringVar(&dt.enabledTools, "enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering", "A comma separated list of tools enabled for this server. Can be overwritten entirely or by disabling specific components, e.g. --disable-search.")
	flag.BoolVar(&dt.search, "disable-search", false, "Disable search tools")
	flag.BoolVar(&dt.datasource, "disable-datasource", false, "Disable datasource tools")
	flag.BoolVar(&dt.incident, "disable-incident", false, "Disable incident tools")
	flag.BoolVar(&dt.prometheus, "disable-prometheus", false, "Disable prometheus tools")
	flag.BoolVar(&dt.loki, "disable-loki", false, "Disable loki tools")
	flag.BoolVar(&dt.elasticsearch, "disable-elasticsearch", false, "Disable elasticsearch tools")
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
	flag.BoolVar(&dt.runpanelquery, "disable-runpanelquery", false, "Disable run panel query tools")
	flag.BoolVar(&dt.graphite, "disable-graphite", false, "Disable Graphite tools")
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

func (dt *disabledTools) addTools(s *server.MCPServer) {
	enabledTools := strings.Split(dt.enabledTools, ",")
	enableWriteTools := !dt.write
	maybeAddTools(s, tools.AddSearchTools, enabledTools, dt.search, "search")
	maybeAddTools(s, tools.AddDatasourceTools, enabledTools, dt.datasource, "datasource")
	maybeAddTools(s, func(mcp *server.MCPServer) { tools.AddIncidentTools(mcp, enableWriteTools) }, enabledTools, dt.incident, "incident")
	maybeAddTools(s, tools.AddPrometheusTools, enabledTools, dt.prometheus, "prometheus")
	maybeAddTools(s, tools.AddLokiTools, enabledTools, dt.loki, "loki")
	maybeAddTools(s, tools.AddElasticsearchTools, enabledTools, dt.elasticsearch, "elasticsearch")
	maybeAddTools(s, tools.AddInfluxDBTools, enabledTools, dt.influxdb, "influxdb")
	maybeAddTools(s, func(mcp *server.MCPServer) { tools.AddAlertingTools(mcp, enableWriteTools) }, enabledTools, dt.alerting, "alerting")
	maybeAddTools(s, func(mcp *server.MCPServer) { tools.AddDashboardTools(mcp, enableWriteTools) }, enabledTools, dt.dashboard, "dashboard")
	maybeAddTools(s, func(mcp *server.MCPServer) { tools.AddFolderTools(mcp, enableWriteTools) }, enabledTools, dt.folder, "folder")
	maybeAddTools(s, tools.AddOnCallTools, enabledTools, dt.oncall, "oncall")
	maybeAddTools(s, tools.AddAssertsTools, enabledTools, dt.asserts, "asserts")
	maybeAddTools(s, func(mcp *server.MCPServer) { tools.AddSiftTools(mcp, enableWriteTools) }, enabledTools, dt.sift, "sift")
	maybeAddTools(s, tools.AddAdminTools, enabledTools, dt.admin, "admin")
	maybeAddTools(s, tools.AddPyroscopeTools, enabledTools, dt.pyroscope, "pyroscope")
	maybeAddTools(s, tools.AddNavigationTools, enabledTools, dt.navigation, "navigation")
	maybeAddTools(s, func(mcp *server.MCPServer) { tools.AddAnnotationTools(mcp, enableWriteTools) }, enabledTools, dt.annotations, "annotations")
	maybeAddTools(s, tools.AddRenderingTools, enabledTools, dt.rendering, "rendering")
	maybeAddTools(s, tools.AddCloudWatchTools, enabledTools, dt.cloudwatch, "cloudwatch")
	maybeAddTools(s, tools.AddExamplesTools, enabledTools, dt.examples, "examples")
	maybeAddTools(s, tools.AddClickHouseTools, enabledTools, dt.clickhouse, "clickhouse")
	maybeAddTools(s, tools.AddRunPanelQueryTools, enabledTools, dt.runpanelquery, "runpanelquery")
	maybeAddTools(s, tools.AddGraphiteTools, enabledTools, dt.graphite, "graphite")
}

func newServer(transport string, dt disabledTools, obs *observability.Observability, sessionIdleTimeoutMinutes int) (*server.MCPServer, *mcpgrafana.ToolManager, *mcpgrafana.SessionManager) {
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

	s = server.NewMCPServer("mcp-grafana", mcpgrafana.Version(),
		server.WithInstructions(`
This server provides access to your Grafana instance and the surrounding ecosystem.

Available Capabilities:
- Dashboards: Search, retrieve, update, and create dashboards. Extract panel queries and datasource information.
- Datasources: List and fetch details for datasources.
- Prometheus & Loki: Run PromQL and LogQL queries, retrieve metric/log metadata, and explore label names/values.
- ClickHouse: Query ClickHouse datasources via Grafana with macro and variable substitution support.
- Elasticsearch: Query Elasticsearch datasources using Lucene syntax or Query DSL for logs and metrics.
- Incidents: Search, create, update, and resolve incidents in Grafana Incident.
- Sift Investigations: Start and manage Sift investigations, analyze logs/traces, find error patterns, and detect slow requests.
- Alerting: List and fetch alert rules and notification contact points.
- OnCall: View and manage on-call schedules, shifts, teams, and users.
- Admin: List teams and perform administrative tasks.
- Pyroscope: Profile applications and fetch profiling data.
- Navigation: Generate deeplink URLs for Grafana resources like dashboards, panels, and Explore queries.
- Rendering: Export dashboard panels or full dashboards as PNG images (requires Grafana Image Renderer plugin).
- Proxied Tools: Access tools from external MCP servers (like Tempo) through dynamic discovery.

Timestamp parameters without a timezone offset are interpreted as UTC. Include an offset like '-05:00' or use relative syntax like 'now-1h' to query in a different timezone.

Note that some of these capabilities may be disabled. Do not try to use features that are not available via tools.
`),
		server.WithHooks(hooks),
	)

	// Initialize ToolManager now that server is created
	stm = mcpgrafana.NewToolManager(sm, s, mcpgrafana.WithProxiedTools(!dt.proxied))

	// Give the SessionManager a reference to the MCPServer so the reaper can
	// unregister sessions from the SDK's internal session map.
	sm.SetMCPServer(s)

	dt.addTools(s)
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

func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig, obs observability.Config, sessionIdleTimeoutMinutes int) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// Set up observability (metrics and tracing)
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

	// Create a client cache for HTTP-based transports to avoid per-request
	// transport allocation (see https://github.com/grafana/mcp-grafana/issues/682).
	var clientCache *mcpgrafana.ClientCache
	if transport != "stdio" {
		clientCache = mcpgrafana.NewClientCache(nil)
		defer clientCache.Close()
	}

	s, tm, sm := newServer(transport, dt, o, sessionIdleTimeoutMinutes)
	defer sm.Close()

	// Create a context that will be cancelled on shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		mux.Handle(basePath, observability.WrapHandler(
			mcpgrafana.ValidateGrafanaURLMiddleware(srv),
			basePath,
		))
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
		mux.Handle(endpointPath, observability.WrapHandler(
			mcpgrafana.ValidateGrafanaURLMiddleware(srv),
			endpointPath,
		))
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
	flag.Parse()

	if *showVersion {
		fmt.Println(mcpgrafana.Version())
		os.Exit(0)
	}

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

	if err := run(transport, *addr, *basePath, *endpointPath, parseLevel(*logLevel), dt, grafanaConfig, tls, obs, *sessionIdleTimeoutMinutes); err != nil {
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
