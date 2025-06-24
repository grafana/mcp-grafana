package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

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

	search, datasource, incident,
	prometheus, loki, alerting,
	dashboard, oncall, asserts, sift, admin,
	pyroscope bool
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
	flag.StringVar(&dt.enabledTools, "enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,oncall,asserts,sift,admin,pyroscope", "A comma separated list of tools enabled for this server. Can be overwritten entirely or by disabling specific components, e.g. --disable-search.")

	flag.BoolVar(&dt.search, "disable-search", false, "Disable search tools")
	flag.BoolVar(&dt.datasource, "disable-datasource", false, "Disable datasource tools")
	flag.BoolVar(&dt.incident, "disable-incident", false, "Disable incident tools")
	flag.BoolVar(&dt.prometheus, "disable-prometheus", false, "Disable prometheus tools")
	flag.BoolVar(&dt.loki, "disable-loki", false, "Disable loki tools")
	flag.BoolVar(&dt.alerting, "disable-alerting", false, "Disable alerting tools")
	flag.BoolVar(&dt.dashboard, "disable-dashboard", false, "Disable dashboard tools")
	flag.BoolVar(&dt.oncall, "disable-oncall", false, "Disable oncall tools")
	flag.BoolVar(&dt.asserts, "disable-asserts", false, "Disable asserts tools")
	flag.BoolVar(&dt.sift, "disable-sift", false, "Disable sift tools")
	flag.BoolVar(&dt.admin, "disable-admin", false, "Disable admin tools")
	flag.BoolVar(&dt.pyroscope, "disable-pyroscope", false, "Disable pyroscope tools")
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
	maybeAddTools(s, tools.AddOnCallTools, enabledTools, dt.oncall, "oncall")
	maybeAddTools(s, tools.AddAssertsTools, enabledTools, dt.asserts, "asserts")
	maybeAddTools(s, tools.AddSiftTools, enabledTools, dt.sift, "sift")
	maybeAddTools(s, tools.AddAdminTools, enabledTools, dt.admin, "admin")
	maybeAddTools(s, tools.AddPyroscopeTools, enabledTools, dt.pyroscope, "pyroscope")
}

func newServer(dt disabledTools) *server.MCPServer {
	s := server.NewMCPServer(
		"mcp-grafana",
		"0.1.0",
	)
	dt.addTools(s)
	return s
}

func run(transport, addr, basePath, endpointPath string, logLevel slog.Level, dt disabledTools, gc *mcpgrafana.GrafanaConfig) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	s := newServer(dt)

	if gc == nil {
		slog.Info("Skipping grafana config")
	}

	switch transport {
	case "stdio":
		srv := server.NewStdioServer(s)
		if gc != nil {
			srv.SetContextFunc(mcpgrafana.ComposedStdioContextFunc(gc))
		} else {
			srv.SetContextFunc(mcpgrafana.ComposeStdioContextFuncs(mcpgrafana.ExtractStdioLocalInfoFromEnv))
		}
		slog.Info("Starting Grafana MCP server using stdio transport")
		return srv.Listen(context.Background(), os.Stdin, os.Stdout)
	case "sse":
		opts := []server.SSEOption{server.WithStaticBasePath(basePath)}
		if gc != nil {
			opts = append(opts, server.WithSSEContextFunc(mcpgrafana.ComposedSSEContextFunc(gc)))
		} else {
			opts = append(opts, server.WithSSEContextFunc(mcpgrafana.ComposeSSEContextFuncs(
				mcpgrafana.ExtractHttpLocalInfoFromEnv,
				mcpgrafana.ExtractAuthorizationFromHeaders,
			)))
		}
		srv := server.NewSSEServer(s, opts...)
		slog.Info("Starting Grafana MCP server using SSE transport", "address", addr, "basePath", basePath)
		if err := srv.Start(addr); err != nil {
			return fmt.Errorf("Server error: %v", err)
		}
	case "streamable-http":
		opts := []server.StreamableHTTPOption{
			server.WithStateLess(true),
			server.WithEndpointPath(endpointPath),
		}
		if gc != nil {
			opts = append(opts, server.WithHTTPContextFunc(mcpgrafana.ComposedHTTPContextFunc(gc)))
		} else {
			opts = append(opts, server.WithHTTPContextFunc(mcpgrafana.ComposeHTTPContextFuncs(
				mcpgrafana.ExtractHttpLocalInfoFromEnv,
				mcpgrafana.ExtractAuthorizationFromHeaders,
			)))
		}
		srv := server.NewStreamableHTTPServer(s, opts...)
		slog.Info("Starting Grafana MCP server using StreamableHTTP transport", "address", addr, "endpointPath", endpointPath)
		if err := srv.Start(addr); err != nil {
			return fmt.Errorf("Server error: %v", err)
		}
	default:
		return fmt.Errorf(
			"Invalid transport type: %s. Must be 'stdio', 'sse' or 'streamable-http'",
			transport,
		)
	}
	return nil
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
	useGrafanaConfig := flag.Bool("use-grafana-config", true, "Use grafana config")
	var dt disabledTools
	dt.addFlags()
	var gc grafanaConfig
	gc.addFlags()
	flag.Parse()

	var config *mcpgrafana.GrafanaConfig
	if *useGrafanaConfig {
		// Convert local grafanaConfig to mcpgrafana.GrafanaConfig
		config = &mcpgrafana.GrafanaConfig{Debug: gc.debug}
		if gc.tlsCertFile != "" || gc.tlsKeyFile != "" || gc.tlsCAFile != "" || gc.tlsSkipVerify {
			config.TLSConfig = &mcpgrafana.TLSConfig{
				CertFile:   gc.tlsCertFile,
				KeyFile:    gc.tlsKeyFile,
				CAFile:     gc.tlsCAFile,
				SkipVerify: gc.tlsSkipVerify,
			}
		}
	}

	if err := run(transport, *addr, *basePath, *endpointPath, parseLevel(*logLevel), dt, config); err != nil {
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
