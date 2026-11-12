package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	// Example 1: Basic TLS configuration with skip verify (for testing)
	fmt.Println("Example 1: Basic TLS configuration with skip verify")
	basicTLSExample()

	// Example 2: Full mTLS configuration with client certificates
	fmt.Println("\nExample 2: Full mTLS configuration")
	fullTLSExample()

	// Example 3: Running an MCP server with TLS support
	fmt.Println("\nExample 3: MCP server with TLS support")
	if len(os.Args) > 1 && os.Args[1] == "run-server" {
		runServerWithTLS()
	} else {
		fmt.Println("Use 'go run tls_example.go run-server' to actually start the server")
		showServerExample()
	}
}

func basicTLSExample() {
	tlsConfig := &mcpgrafana.TLSConfig{SkipVerify: true}

	grafanaConfig := mcpgrafana.GrafanaConfig{
		Debug:     true,
		TLSConfig: tlsConfig,
	}

	contextFunc := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)

	ctx := contextFunc(context.Background())

	retrievedConfig := mcpgrafana.GrafanaConfigFromContext(ctx)
	if retrievedConfig.TLSConfig != nil {
		fmt.Printf("✓ TLS configuration applied: SkipVerify=%v\n", retrievedConfig.TLSConfig.SkipVerify)
	}

	fmt.Printf("✓ Debug mode enabled: %v\n", retrievedConfig.Debug)
}

func fullTLSExample() {
	certFile := "/path/to/client.crt"
	keyFile := "/path/to/client.key"
	caFile := "/path/to/ca.crt"

	tlsConfig := &mcpgrafana.TLSConfig{
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   caFile,
	}

	grafanaConfig := mcpgrafana.GrafanaConfig{
		Debug:     false,
		TLSConfig: tlsConfig,
	}

	fmt.Printf("✓ TLS configuration created:\n")
	fmt.Printf("  - Client cert: %s\n", tlsConfig.CertFile)
	fmt.Printf("  - Client key: %s\n", tlsConfig.KeyFile)
	fmt.Printf("  - CA file: %s\n", tlsConfig.CAFile)
	fmt.Printf("  - Skip verify: %v\n", tlsConfig.SkipVerify)
	fmt.Printf("  - Debug mode: %v\n", grafanaConfig.Debug)

	stdioFunc := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)
	httpFunc := mcpgrafana.ComposedHTTPContextFunc(grafanaConfig)

	fmt.Printf("✓ Context functions created for all transport types\n")

	_ = stdioFunc
	_ = httpFunc
}

func showServerExample() {
	fmt.Println("Example MCP server configuration with TLS:")
	fmt.Println(`// Create TLS configuration
tlsConfig := &mcpgrafana.TLSConfig{
    CertFile: "/path/to/client.crt",
    KeyFile:  "/path/to/client.key",
    CAFile:   "/path/to/ca.crt",
}

// Create Grafana configuration
grafanaConfig := mcpgrafana.GrafanaConfig{
    Debug: true,
    TLSConfig: tlsConfig,
}

// Create MCP server
s := mcp.NewServer(&mcp.Implementation{Name: "mcp-grafana", Version: "1.0.0"}, nil)

// Add tools
tools.AddSearchTools(s)
tools.AddDatasourceTools(s, false)

// Set up context and run
cf := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)
ctx := cf(context.Background())
s.Run(ctx, &mcp.StdioTransport{})`)
}

func runServerWithTLS() {
	if os.Getenv("GRAFANA_URL") == "" {
		if err := os.Setenv("GRAFANA_URL", "https://localhost:3000"); err != nil {
			log.Printf("Failed to set GRAFANA_URL: %v", err)
		}
	}
	if os.Getenv("GRAFANA_SERVICE_ACCOUNT_TOKEN") == "" {
		if os.Getenv("GRAFANA_API_KEY") == "" {
			fmt.Println("Warning: Neither GRAFANA_SERVICE_ACCOUNT_TOKEN nor GRAFANA_API_KEY is set")
		} else {
			fmt.Println("Warning: GRAFANA_API_KEY is deprecated, please use GRAFANA_SERVICE_ACCOUNT_TOKEN instead")
		}
	}

	tlsConfig := &mcpgrafana.TLSConfig{SkipVerify: true}
	grafanaConfig := mcpgrafana.GrafanaConfig{
		Debug:     true,
		TLSConfig: tlsConfig,
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "mcp-grafana-tls-example", Version: "1.0.0"}, nil)

	tools.AddSearchTools(s)
	tools.AddDatasourceTools(s, false)
	tools.AddDashboardTools(s, false)

	cf := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)
	ctx := cf(context.Background())

	fmt.Printf("Starting MCP Grafana server with TLS support...\n")
	fmt.Printf("Grafana URL: %s\n", os.Getenv("GRAFANA_URL"))
	fmt.Printf("TLS Skip Verify: %v\n", tlsConfig.SkipVerify)

	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func customClientExample() { //nolint:unused // Example function for documentation
	ctx := context.Background()

	tlsConfig := &mcpgrafana.TLSConfig{
		CertFile: "/path/to/cert.pem",
		KeyFile:  "/path/to/key.pem",
		CAFile:   "/path/to/ca.pem",
	}
	config := mcpgrafana.GrafanaConfig{
		TLSConfig: tlsConfig,
	}
	ctx = mcpgrafana.WithGrafanaConfig(ctx, config)
	_ = ctx

	transport, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}

	_ = transport
	fmt.Println("✓ Custom HTTP transport created with TLS configuration")
}
