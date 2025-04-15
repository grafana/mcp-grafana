package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/tools"
)

func maybeAddTools(s *server.MCPServer, tf func(*server.MCPServer), disable bool, category string) {
	if disable {
		slog.Info("Disabling tools", "category", category)
		return
	}
	tf(s)
}

// disabledTools indicates whether each category of tools should be disabled.
type disabledTools struct {
	search, datasource, incident,
	prometheus, loki, alerting,
	dashboard, oncall bool
}

func (dt *disabledTools) addFlags() {
	flag.BoolVar(&dt.search, "disable-search", false, "Disable search tools")
	flag.BoolVar(&dt.datasource, "disable-datasource", false, "Disable datasource tools")
	flag.BoolVar(&dt.incident, "disable-incident", false, "Disable incident tools")
	flag.BoolVar(&dt.prometheus, "disable-prometheus", false, "Disable prometheus tools")
	flag.BoolVar(&dt.loki, "disable-loki", false, "Disable loki tools")
	flag.BoolVar(&dt.alerting, "disable-alerting", false, "Disable alerting tools")
	flag.BoolVar(&dt.dashboard, "disable-dashboard", false, "Disable dashboard tools")
	flag.BoolVar(&dt.oncall, "disable-oncall", false, "Disable oncall tools")
}

func (dt *disabledTools) addTools(s *server.MCPServer) {
	maybeAddTools(s, tools.AddSearchTools, dt.search, "search")
	maybeAddTools(s, tools.AddDatasourceTools, dt.datasource, "datasource")
	maybeAddTools(s, tools.AddIncidentTools, dt.incident, "incident")
	maybeAddTools(s, tools.AddPrometheusTools, dt.prometheus, "prometheus")
	maybeAddTools(s, tools.AddLokiTools, dt.loki, "loki")
	maybeAddTools(s, tools.AddAlertingTools, dt.alerting, "alerting")
	maybeAddTools(s, tools.AddDashboardTools, dt.dashboard, "dashboard")
	maybeAddTools(s, tools.AddOnCallTools, dt.oncall, "oncall")
}

func newServer(dt disabledTools) *server.MCPServer {
	s := server.NewMCPServer(
		"mcp-grafana",
		"0.1.0",
	)
	dt.addTools(s)
	return s
}

func run(transport, addr string, logLevel slog.Level, dt disabledTools) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	s := newServer(dt)

	switch transport {
	case "stdio":
		srv := server.NewStdioServer(s)
		srv.SetContextFunc(mcpgrafana.ComposedStdioContextFunc)
		slog.Info("Starting Grafana MCP server using stdio transport")
		return srv.Listen(context.Background(), os.Stdin, os.Stdout)
	case "sse":
		srv := server.NewSSEServer(s,
			server.WithSSEContextFunc(mcpgrafana.ComposedSSEContextFunc),
		)
		slog.Info("Starting Grafana MCP server using SSE transport", "address", addr)
		if err := srv.Start(addr); err != nil {
			return fmt.Errorf("Server error: %v", err)
		}
	default:
		return fmt.Errorf(
			"Invalid transport type: %s. Must be 'stdio' or 'sse'",
			transport,
		)
	}
	return nil
}

func main() {
	var transport string
	flag.StringVar(&transport, "t", "stdio", "Transport type (stdio or sse)")
	flag.StringVar(
		&transport,
		"transport",
		"stdio",
		"Transport type (stdio or sse)",
	)
	addr := flag.String("sse-address", "localhost:8000", "The host and port to start the sse server on")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	var dt disabledTools
	dt.addFlags()
	flag.Parse()

	if err := run(transport, *addr, parseLevel(*logLevel), dt); err != nil {
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
