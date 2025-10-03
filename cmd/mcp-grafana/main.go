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

		"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/tools"
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
	dynamicTools bool

	search, datasource, incident,
	prometheus, loki, alerting,
	dashboard, folder, oncall, asserts, sift, admin,
	pyroscope, navigation bool
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
}

func (dt *disabledTools) addFlags() {
	flag.StringVar(&dt.enabledTools, "enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,admin,pyroscope,navigation", "A comma separated list of tools enabled for this server. Can be overwritten entirely or by disabling specific components, e.g. --disable-search.")
	flag.BoolVar(&dt.dynamicTools, "dynamic-toolsets", getEnvBool("GRAFANA_DYNAMIC_TOOLSETS", false), "Enable dynamic tool discovery. When enabled, only discovery tools are registered initially, and other toolsets can be enabled on-demand.")

	flag.BoolVar(&dt.search, "disable-search", false, "Disable search tools")
	flag.BoolVar(&dt.datasource, "disable-datasource", false, "Disable datasource tools")
	flag.BoolVar(&dt.incident, "disable-incident", false, "Disable incident tools")
	flag.BoolVar(&dt.prometheus, "disable-prometheus", false, "Disable prometheus tools")
	flag.BoolVar(&dt.loki, "disable-loki", false, "Disable loki tools")
	flag.BoolVar(&dt.alerting, "disable-alerting", false, "Disable alerting tools")
	flag.BoolVar(&dt.dashboard, "disable-dashboard", false, "Disable dashboard tools")
	flag.BoolVar(&dt.folder, "disable-folder", false, "Disable folder tools")
	flag.BoolVar(&dt.oncall, "disable-oncall", false, "Disable oncall tools")
	flag.BoolVar(&dt.asserts, "disable-asserts", false, "Disable asserts tools")
	flag.BoolVar(&dt.sift, "disable-sift", false, "Disable sift tools")
	flag.BoolVar(&dt.admin, "disable-admin", false, "Disable admin tools")
	flag.BoolVar(&dt.pyroscope, "disable-pyroscope", false, "Disable pyroscope tools")
	flag.BoolVar(&dt.navigation, "disable-navigation", false, "Disable navigation tools")
}

func (gc *grafanaConfig) addFlags() {
	flag.BoolVar(&gc.debug, "debug", false, "Enable debug mode for the Grafana transport")

	// TLS configuration flags
	flag.StringVar(&gc.tlsCertFile, "tls-cert-file", "", "Path to TLS certificate file for client authentication")
	flag.StringVar(&gc.tlsKeyFile, "tls-key-file", "", "Path to TLS private key file for client authentication")
	flag.StringVar(&gc.tlsCAFile, "tls-ca-file", "", "Path to TLS CA certificate file for server verification")
	flag.BoolVar(&gc.tlsSkipVerify, "tls-skip-verify", false, "Skip TLS certificate verification (insecure)")
}

func (dt *disabledTools) addTools(s *server.MCPServer) {
	enabledTools := strings.Split(dt.enabledTools, ",")
	maybeAddTools(s, tools.AddSearchTools, enabledTools, dt.search, "search")
	maybeAddTools(s, tools.AddDatasourceTools, enabledTools, dt.datasource, "datasource")
	maybeAddTools(s, tools.AddIncidentTools, enabledTools, dt.incident, "incident")
	maybeAddTools(s, tools.AddPrometheusTools, enabledTools, dt.prometheus, "prometheus")
	maybeAddTools(s, tools.AddLokiTools, enabledTools, dt.loki, "loki")
	maybeAddTools(s, tools.AddAlertingTools, enabledTools, dt.alerting, "alerting")
	maybeAddTools(s, tools.AddDashboardTools, enabledTools, dt.dashboard, "dashboard")
	maybeAddTools(s, tools.AddFolderTools, enabledTools, dt.folder, "folder")
	maybeAddTools(s, tools.AddOnCallTools, enabledTools, dt.oncall, "oncall")
	maybeAddTools(s, tools.AddAssertsTools, enabledTools, dt.asserts, "asserts")
	maybeAddTools(s, tools.AddSiftTools, enabledTools, dt.sift, "sift")
	maybeAddTools(s, tools.AddAdminTools, enabledTools, dt.admin, "admin")
	maybeAddTools(s, tools.AddPyroscopeTools, enabledTools, dt.pyroscope, "pyroscope")
	maybeAddTools(s, tools.AddNavigationTools, enabledTools, dt.navigation, "navigation")
}

// NEW: addToolsDynamically sets up dynamic tool discovery
func (dt *disabledTools) addToolsDynamically(s *server.MCPServer) *mcpgrafana.DynamicToolManager {
	dtm := mcpgrafana.NewDynamicToolManager(s)

	// Parse enabled tools list
	enabledTools := strings.Split(dt.enabledTools, ",")

	// Helper function to check if a tool is enabled
	isEnabled := func(toolName string) bool {
		// If enabledTools is empty string, no tools should be available
		if dt.enabledTools == "" {
			return false
		}
		return slices.Contains(enabledTools, toolName)
	}

	// Define all available toolsets
	allToolsets := []struct {
		name        string
		description string
		addFunc     func(*server.MCPServer)
	}{
		{"search", "Tools for searching dashboards, folders, and other Grafana resources", tools.AddSearchTools},
		{"datasource", "Tools for listing and fetching datasource details", tools.AddDatasourceTools},
		{"incident", "Tools for managing Grafana Incident (create, update, search incidents)", tools.AddIncidentTools},
		{"prometheus", "Tools for querying Prometheus metrics and metadata", tools.AddPrometheusTools},
		{"loki", "Tools for querying Loki logs and labels", tools.AddLokiTools},
		{"alerting", "Tools for managing alert rules and notification contact points", tools.AddAlertingTools},
		{"dashboard", "Tools for managing Grafana dashboards (get, update, extract queries)", tools.AddDashboardTools},
		{"folder", "Tools for managing Grafana folders", tools.AddFolderTools},
		{"oncall", "Tools for managing OnCall schedules, shifts, teams, and users", tools.AddOnCallTools},
		{"asserts", "Tools for Grafana Asserts cloud functionality", tools.AddAssertsTools},
		{"sift", "Tools for Sift investigations (analyze logs/traces, find errors, detect slow requests)", tools.AddSiftTools},
		{"admin", "Tools for administrative tasks (list teams, manage users)", tools.AddAdminTools},
		{"pyroscope", "Tools for profiling applications with Pyroscope", tools.AddPyroscopeTools},
		{"navigation", "Tools for generating deeplink URLs to Grafana resources", tools.AddNavigationTools},
	}

	// Only register toolsets that are enabled
	for _, toolset := range allToolsets {
		if isEnabled(toolset.name) {
			dtm.RegisterToolset(&mcpgrafana.Toolset{
				Name:        toolset.name,
				Description: toolset.description,
				AddFunc:     toolset.addFunc,
			})
		}
	}

	// Add the dynamic discovery tools themselves
	mcpgrafana.AddDynamicDiscoveryTools(dtm, s)

	return dtm
}

func newServer(dt disabledTools) *server.MCPServer {
	var instructions string
	if dt.dynamicTools {
		instructions = `
	This server provides access to your Grafana instance and the surrounding ecosystem with dynamic tool discovery.

	Getting Started:
	1. Use 'grafana_list_toolsets' to see all available toolsets
	2. Use 'grafana_enable_toolset' to enable specific functionality you need
	3. Once enabled, the toolset's tools will be available for use

	Available Toolset Categories:
	- search: Search dashboards, folders, and resources
	- datasource: Manage datasources
	- prometheus: Query Prometheus metrics
	- loki: Query Loki logs
	- dashboard: Manage dashboards
	- folder: Manage folders
	- incident: Manage incidents
	- alerting: Manage alerts
	- oncall: Manage OnCall schedules
	- asserts: Grafana Asserts functionality
	- sift: Sift investigations
	- admin: Administrative tasks
	- pyroscope: Application profiling
	- navigation: Generate deeplinks
	`
	} else {
		instructions = `
	This server provides access to your Grafana instance and the surrounding ecosystem.

	Available Capabilities:
	- Dashboards: Search, retrieve, update, and create dashboards. Extract panel queries and datasource information.
	- Datasources: List and fetch details for datasources.
	- Prometheus & Loki: Run PromQL and LogQL queries, retrieve metric/log metadata, and explore label names/values.
	- Incidents: Search, create, update, and resolve incidents in Grafana Incident.
	- Sift Investigations: Start and manage Sift investigations, analyze logs/traces, find error patterns, and detect slow requests.
	- Alerting: List and fetch alert rules and notification contact points.
	- OnCall: View and manage on-call schedules, shifts, teams, and users.
	- Admin: List teams and perform administrative tasks.
	- Pyroscope: Profile applications and fetch profiling data.
	- Navigation: Generate deeplink URLs for Grafana resources like dashboards, panels, and Explore queries.
	`
	}

	// Create server with tool capabilities enabled for dynamic tool discovery
	s := server.NewMCPServer("mcp-grafana", mcpgrafana.Version(),
		server.WithInstructions(instructions),
		server.WithToolCapabilities(true)) // Enable listChanged notifications

	if dt.dynamicTools {
		// For dynamic toolsets, start with only discovery tools
		// Tools will be added dynamically when toolsets are enabled
		dt.addToolsDynamically(s)
	} else {
		// Use static tool registration
		dt.addTools(s)
	}

	return s
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

func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc mcpgrafana.GrafanaConfig, tls tlsConfig) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	s := newServer(dt)

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
		srv.SetContextFunc(mcpgrafana.ComposedStdioContextFunc(gc))
		slog.Info("Starting Grafana MCP server using stdio transport", "version", mcpgrafana.Version())

		err := srv.Listen(ctx, os.Stdin, os.Stdout)
		if err != nil && err != context.Canceled {
			return fmt.Errorf("server error: %v", err)
		}
		return nil

	case "sse":
		srv := server.NewSSEServer(s,
			server.WithSSEContextFunc(mcpgrafana.ComposedSSEContextFunc(gc)),
			server.WithStaticBasePath(basePath),
		)
		slog.Info("Starting Grafana MCP server using SSE transport",
			"version", mcpgrafana.Version(), "address", addr, "basePath", basePath)
		return runHTTPServer(ctx, srv, addr, "SSE")
	case "streamable-http":
		opts := []server.StreamableHTTPOption{
			server.WithHTTPContextFunc(mcpgrafana.ComposedHTTPContextFunc(gc)),
			server.WithStateLess(true),
			server.WithEndpointPath(endpointPath),
		}
		if tls.certFile != "" || tls.keyFile != "" {
			opts = append(opts, server.WithTLSCert(tls.certFile, tls.keyFile))
		}
		srv := server.NewStreamableHTTPServer(s, opts...)
		slog.Info("Starting Grafana MCP server using StreamableHTTP transport",
			"version", mcpgrafana.Version(), "address", addr, "endpointPath", endpointPath)
		return runHTTPServer(ctx, srv, addr, "StreamableHTTP")
	default:
		return fmt.Errorf(
			"invalid transport type: %s. Must be 'stdio', 'sse' or 'streamable-http'",
			transport,
		)
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
	showVersion := flag.Bool("version", false, "Print the version and exit")
	var dt disabledTools
	dt.addFlags()
	var gc grafanaConfig
	gc.addFlags()
	var tls tlsConfig
	tls.addFlags()
	flag.Parse()

	if *showVersion {
		fmt.Println(mcpgrafana.Version())
		os.Exit(0)
	}

	// Convert local grafanaConfig to mcpgrafana.GrafanaConfig
	grafanaConfig := mcpgrafana.GrafanaConfig{Debug: gc.debug}
	if gc.tlsCertFile != "" || gc.tlsKeyFile != "" || gc.tlsCAFile != "" || gc.tlsSkipVerify {
		grafanaConfig.TLSConfig = &mcpgrafana.TLSConfig{
			CertFile:   gc.tlsCertFile,
			KeyFile:    gc.tlsKeyFile,
			CAFile:     gc.tlsCAFile,
			SkipVerify: gc.tlsSkipVerify,
		}
	}

	if err := run(transport, *addr, *basePath, *endpointPath, parseLevel(*logLevel), dt, grafanaConfig, tls); err != nil {
		panic(err)
	}
}

// getEnvBool reads a boolean from an environment variable
func getEnvBool(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists {
		return value == "1" || strings.ToLower(value) == "true"
	}
	return defaultValue
}

func parseLevel(level string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}
	return l
}
