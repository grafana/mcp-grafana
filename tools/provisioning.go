package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		Git struct {
			URL    string `json:"url"`
			Branch string `json:"branch"`
			Path   string `json:"path"`
		} `json:"git"`
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
	if err := validateRepoSlug("namespace", ns); err != nil {
		return nil, err
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	endpoint := strings.TrimRight(cfg.URL, "/") +
		fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/%s/repositories", url.PathEscape(ns))

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
		case "git":
			r.URL = item.Spec.Git.URL
			r.Branch = item.Spec.Git.Branch
			r.Path = item.Spec.Git.Path
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

// ValidateProvisioningFileParams identifies a single file to validate inside
// a provisioning repository at a specific ref.
type ValidateProvisioningFileParams struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"description=Kubernetes-style namespace to read the repository from. Defaults to 'default'."`
	Repo      string `json:"repo" jsonschema:"required,description=Provisioning repository slug. Get one from list_provisioning_repositories."`
	Path      string `json:"path" jsonschema:"required,description=File path within the repository (e.g. 'folder/dashboard.json')."`
	Ref       string `json:"ref,omitempty" jsonschema:"description=Branch or commit SHA. Defaults to the repository's main branch."`
}

// ProvisioningResourceType captures the GVK+resource of the file's target.
type ProvisioningResourceType struct {
	Group    string `json:"group,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Resource string `json:"resource,omitempty"`
	Version  string `json:"version,omitempty"`
}

// ProvisioningValidationError is a single validation issue. When the server
// returns a k8s Status with details.causes\\, each cause becomes one of these;
// otherwise we surface a single entry built from the Status message.
type ProvisioningValidationError struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
}

// ProvisioningFileValidation is the validate_provisioning_file response.
type ProvisioningFileValidation struct {
	Valid  bool                          `json:"valid"`
	Action string                        `json:"action,omitempty"`
	Type   *ProvisioningResourceType     `json:"type,omitempty"`
	Errors []ProvisioningValidationError `json:"errors,omitempty"`
}

// validateRepoSlug rejects repo values that would change the URL structure.
// fieldName is the user-facing parameter name to embed in error messages so
// the caller knows which argument was rejected (e.g. "repo" vs
// "provisioningPreview.repo").
func validateRepoSlug(fieldName, repo string) error {
	if repo == "" {
		return fmt.Errorf("%s is required", fieldName)
	}
	if strings.ContainsAny(repo, `/\`) {
		return fmt.Errorf("%s must not contain path separators", fieldName)
	}
	// Reject both ".." and "." since HTTP intermediaries that normalize
	// these segments could collapse them out and redirect the request.
	if repo == "." || repo == ".." {
		return fmt.Errorf("%s must not be a relative-directory reference", fieldName)
	}
	return nil
}

// validateRepoPath rejects path values with parent-directory or
// current-directory segments to keep HTTP intermediaries that normalize
// `..` or `.` from collapsing the path and redirecting the request.
// Backslash is normalized to a forward slash before the segment scan so
// a value like `a\..\b` is rejected the same way `a/../b` is.
func validateRepoPath(fieldName, path string) error {
	if path == "" {
		return fmt.Errorf("%s is required", fieldName)
	}
	normalized := strings.ReplaceAll(path, `\`, "/")
	// A path of only separators (e.g. "/" or "///") has no real segment and
	// would build a file URL with an empty trailing segment.
	if strings.Trim(normalized, "/") == "" {
		return fmt.Errorf("%s must reference a file", fieldName)
	}
	for _, seg := range strings.Split(normalized, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%s must not contain relative-directory segments", fieldName)
		}
	}
	return nil
}

// k8sStatus mirrors the subset of metav1.Status we care about for surfacing
// validation errors back to the caller.
type k8sStatus struct {
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
	Code    int    `json:"code"`
	Details struct {
		Group  string `json:"group"`
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Causes []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
			Field   string `json:"field"`
		} `json:"causes"`
	} `json:"details"`
}

// fileValidationResponse mirrors the success-shape returned by the files
// endpoint: a ResourceWrapper carrying the resource action/type and any
// non-fatal parser errors. We intentionally drop the file/dryRun payloads —
// the agent already has the raw file via git, and the normalized dryRun
// is rarely actionable.
type fileValidationResponse struct {
	Errors   []string `json:"errors"`
	Resource struct {
		Action string                   `json:"action"`
		Type   ProvisioningResourceType `json:"type"`
	} `json:"resource"`
}

func validateProvisioningFile(ctx context.Context, args ValidateProvisioningFileParams) (*ProvisioningFileValidation, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	if cfg.URL == "" {
		return nil, fmt.Errorf("grafana URL is not configured")
	}
	if err := validateRepoSlug("repo", args.Repo); err != nil {
		return nil, err
	}
	if err := validateRepoPath("path", args.Path); err != nil {
		return nil, err
	}

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	ns := args.Namespace
	if ns == "" {
		ns = "default"
	}
	if err := validateRepoSlug("namespace", ns); err != nil {
		return nil, err
	}
	escapedPath := (&url.URL{Path: strings.TrimLeft(args.Path, "/")}).EscapedPath()
	endpoint := strings.TrimRight(cfg.URL, "/") + fmt.Sprintf(
		"/apis/provisioning.grafana.app/v0alpha1/namespaces/%s/repositories/%s/files/%s",
		url.PathEscape(ns), url.PathEscape(args.Repo), escapedPath,
	)
	if args.Ref != "" {
		endpoint += "?" + url.Values{"ref": []string{args.Ref}}.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Success / partial-content path: file parses and dry-run apply succeeded
	// (the latter is implied by a 2xx response from this endpoint).
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
		var raw fileValidationResponse
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		out := &ProvisioningFileValidation{
			Valid:  len(raw.Errors) == 0,
			Action: raw.Resource.Action,
		}
		if raw.Resource.Type != (ProvisioningResourceType{}) {
			t := raw.Resource.Type
			out.Type = &t
		}
		for _, msg := range raw.Errors {
			out.Errors = append(out.Errors, ProvisioningValidationError{Message: msg})
		}
		return out, nil
	}

	// A 4xx admission rejection comes back as a k8s Status object carrying the
	// validation errors (typically 422). Surface those as valid:false. Other
	// statuses — notably 5xx server errors — are transient failures, not a
	// verdict on the file, so they bubble up as a tool error even when the
	// body happens to be a Status object.
	var status k8sStatus
	if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
		json.Unmarshal(body, &status) == nil && status.Kind == "Status" {
		out := &ProvisioningFileValidation{Valid: false}
		if len(status.Details.Causes) > 0 {
			for _, c := range status.Details.Causes {
				out.Errors = append(out.Errors, ProvisioningValidationError{
					Field:   c.Field,
					Message: c.Message,
					Reason:  c.Reason,
				})
			}
		} else {
			out.Errors = append(out.Errors, ProvisioningValidationError{
				Message: status.Message,
				Reason:  status.Reason,
			})
		}
		return out, nil
	}

	return nil, fmt.Errorf("validate file: HTTP %d - %s", resp.StatusCode, string(body))
}

var ValidateProvisioningFile = mcpgrafana.MustTool(
	"validate_provisioning_file",
	"Validate a file in a provisioning repository at a given branch or commit by dry-run applying it. "+
		"Returns whether the file would be accepted (valid)\\, what resource action would result (create/update)\\, the target resource type\\, and any structured validation errors. "+
		"Use to confirm a draft dashboard or other resource will be accepted before merging or applying a PR — this is the same validation surface that Grafana's PR commenter reports.",
	validateProvisioningFile,
	mcp.WithTitleAnnotation("Validate provisioning file"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

func AddProvisioningTools(s *server.MCPServer) {
	ListProvisioningRepositories.Register(s)
	ValidateProvisioningFile.Register(s)
}
