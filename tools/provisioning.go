package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// ListProvisioningRepositoriesParams accepts an optional namespace.
type ListProvisioningRepositoriesParams struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"description=Kubernetes-style namespace to list repositories from. Defaults to 'default' which matches single-tenant Grafana deployments."`
}

// ProvisioningRepository is a concise summary of a single repository, suitable
// for an agent picking a repo slug to pass to other tools (e.g. as the
// provisioningPreview.repo argument to get_panel_image).
type ProvisioningRepository struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Type        string   `json:"type"`
	URL         string   `json:"url,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	Path        string   `json:"path,omitempty"`
	SyncEnabled bool     `json:"syncEnabled"`
	Workflows   []string `json:"workflows,omitempty"`
	Healthy     bool     `json:"healthy"`
	SyncState   string   `json:"syncState,omitempty"`
}

// raw response shapes — only the fields we care about.
type repositoryListResponse struct {
	Items []repositoryItem `json:"items"`
}

type repositoryItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Title  string `json:"title"`
		Type   string `json:"type"`
		GitHub struct {
			URL    string `json:"url"`
			Branch string `json:"branch"`
			Path   string `json:"path"`
		} `json:"github"`
		Local struct {
			Path string `json:"path"`
		} `json:"local"`
		Sync struct {
			Enabled bool `json:"enabled"`
		} `json:"sync"`
		Workflows []string `json:"workflows"`
	} `json:"spec"`
	Status struct {
		Health struct {
			Healthy bool `json:"healthy"`
		} `json:"health"`
		Sync struct {
			State string `json:"state"`
		} `json:"sync"`
	} `json:"status"`
}

func listProvisioningRepositories(ctx context.Context, args ListProvisioningRepositoriesParams) ([]ProvisioningRepository, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}

	ns := args.Namespace
	if ns == "" {
		ns = "default"
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	endpoint := strings.TrimRight(cfg.URL, "/") +
		fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/%s/repositories", ns)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("list repositories: HTTP %d - %s", resp.StatusCode, string(body))
	}

	respBody, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var list repositoryListResponse
	if err := json.Unmarshal(respBody, &list); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	out := make([]ProvisioningRepository, 0, len(list.Items))
	for _, item := range list.Items {
		r := ProvisioningRepository{
			Name:        item.Metadata.Name,
			Title:       item.Spec.Title,
			Type:        item.Spec.Type,
			SyncEnabled: item.Spec.Sync.Enabled,
			Workflows:   item.Spec.Workflows,
			Healthy:     item.Status.Health.Healthy,
			SyncState:   item.Status.Sync.State,
		}
		switch item.Spec.Type {
		case "github":
			r.URL = item.Spec.GitHub.URL
			r.Branch = item.Spec.GitHub.Branch
			r.Path = item.Spec.GitHub.Path
		case "local":
			r.Path = item.Spec.Local.Path
		}
		out = append(out, r)
	}
	return out, nil
}

var ListProvisioningRepositories = mcpgrafana.MustTool(
	"list_provisioning_repositories",
	"List provisioning repositories (e.g. git-sync sources) configured for this Grafana instance. "+
		"Returns each repository's slug along with its source URL, branch, path, sync state, and health. "+
		"Use the returned `name` as the `repo` argument when rendering a not-yet-applied dashboard preview via get_panel_image's provisioningPreview parameter.",
	listProvisioningRepositories,
	mcp.WithTitleAnnotation("List provisioning repositories"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddProvisioningTools(s *server.MCPServer) {
	ListProvisioningRepositories.Register(s)
}
