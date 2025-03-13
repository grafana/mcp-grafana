package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/tools"
)

func newServer() *server.MCPServer {
	s := server.NewMCPServer(
		"mcp-grafana",
		"0.1.0",
	)
	tools.AddSearchTools(s)
	tools.AddDatasourceTools(s)
	tools.AddIncidentTools(s)
	tools.AddPrometheusTools(s)
	return s
}

func run(transport, addr string) error {
	s := newServer()

	switch transport {
	case "stdio":
		srv := server.NewStdioServer(s)
		srv.SetContextFunc(mcpgrafana.ComposedStdioContextFunc)
		return srv.Listen(context.Background(), os.Stdin, os.Stdout)
	case "sse":
		srv := server.NewSSEServer(s,
			server.WithSSEContextFunc(mcpgrafana.ComposedSSEContextFunc),
		)
		log.Printf("SSE server listening on %s", addr)
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
	flag.Parse()

	if err := run(transport, *addr); err != nil {
		panic(err)
	}
}
