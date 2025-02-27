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
	return s
}

func run(transport string) error {
	s := newServer()

	switch transport {
	case "stdio":
		srv := server.NewStdioServer(s)
		srv.SetContextFunc(mcpgrafana.ExtractClientFromEnv)
		return srv.Listen(context.Background(), os.Stdin, os.Stdout)
	case "sse":
		addr := "http://localhost:8080"
		srv := server.NewSSEServer(s, addr)
		srv.SetContextFunc(mcpgrafana.ExtractClientFromHeaders)
		log.Printf("SSE server listening on %s", addr)
		if err := srv.Start("localhost:8080"); err != nil {
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
	flag.Parse()

	if err := run(transport); err != nil {
		panic(err)
	}
}
