package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func AddProvisioningRepositoryFilesTool(mcp *server.MCPServer) {
	ListProvisioningRepositoryFiles.Register(mcp)
}

const listProvisioningRepositoryFilesToolPrompt = `List files within a specific Git repository configured for this Grafana instance. This tool shows all files present in the repository at the configured path and branch, including dashboards and folders. IMPORTANT: If any files are found, it means this Grafana instance IS managed by Git (GitOps). If no files are found, the repository may be empty or the instance is NOT Git-managed. Repository files are used for managing Grafana configuration as code (dashboards, datasources, etc.) through Git version control. Requires a repository_name parameter for exact matching, and optionally supports filtering by file path using regex patterns.`

var ListProvisioningRepositoryFiles = mcpgrafana.MustTool(
	"list_provisioning_repository_files",
	listProvisioningRepositoryFilesToolPrompt,
	listProvisioningRepositoryFiles,
	mcp.WithTitleAnnotation("List Provisioning Repository Files"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListProvisioningRepositoryFilesParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Repository name for exact matching"`
	Path           string `json:"path,omitempty" jsonschema:"description=Repository file path (can be a javascript regex pattern)"`
}

type RepositoryFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Hash string `json:"hash"`
}

type ProvisioningRepositoryFilesResponse struct {
	Items []RepositoryFile `json:"items"`
}

func formatRepositoryFile(f RepositoryFile) string {
	// Format size in a human-readable way
	sizeStr := formatFileSize(f.Size)
	return fmt.Sprintf("- path=%s | size=%s | hash=%s", f.Path, sizeStr, f.Hash[:8]+"...")
}

func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func listProvisioningRepositoryFiles(ctx context.Context, args ListProvisioningRepositoryFilesParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Construct the API URL with the repository name
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s/files", args.RepositoryName)
	url := fmt.Sprintf("%s%s", strings.TrimRight(cfg.URL, "/"), apiPath)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	// Add authorization header
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	} else if cfg.AccessToken != "" && cfg.IDToken != "" {
		req.Header.Set("X-Access-Token", cfg.AccessToken)
		req.Header.Set("X-Grafana-Id", cfg.IDToken)
	}

	// Create HTTP client with TLS configuration if available
	client := &http.Client{}
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		transport, err := tlsConfig.HTTPTransport(http.DefaultTransport.(*http.Transport))
		if err != nil {
			return "", fmt.Errorf("failed to create custom transport: %w", err)
		}
		client.Transport = transport
	}

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Sprintf("Repository '%s' not found or does not exist.", args.RepositoryName), nil
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response ProvisioningRepositoryFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// If no files found
	if len(response.Items) == 0 {
		return "Repository is empty", nil
	}

	// Apply path filter if provided
	filtered := response.Items

	if args.Path != "" {
		var pathFiltered []RepositoryFile
		// Try to compile as regex first
		pathRegex, err := regexp.Compile("(?i)" + args.Path)
		if err != nil {
			// If regex compilation fails, use simple string matching
			for _, f := range filtered {
				if strings.Contains(strings.ToLower(f.Path), strings.ToLower(args.Path)) {
					pathFiltered = append(pathFiltered, f)
				}
			}
		} else {
			// Use regex matching
			for _, f := range filtered {
				if pathRegex.MatchString(f.Path) {
					pathFiltered = append(pathFiltered, f)
				}
			}
		}
		filtered = pathFiltered
	}

	// Check if filtering resulted in no matches
	if len(filtered) == 0 {
		return "No repository files found matching the criteria.", nil
	}

	// Format the results
	countHeader := fmt.Sprintf("Found %d repository files.", len(filtered))

	var rows []string
	rows = append(rows, countHeader)

	for _, f := range filtered {
		rows = append(rows, formatRepositoryFile(f))
	}

	return strings.Join(rows, "\n"), nil
}
