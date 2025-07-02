// Package tools provides MCP tools for managing Grafana provisioning repositories.
//
// Example usage for creating a pull request:
//
//	echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "create_provisioning_repository_pr", "arguments": {"repository_name": "my-dashboard-repo", "title": "Add new CPU monitoring dashboard", "body": "This PR adds a comprehensive CPU monitoring dashboard with alerts for high usage", "ref": "feature/cpu-dashboard"}}}' | go run cmd/mcp-grafana/main.go -t stdio

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func AddProvisioningRepositoriesTool(mcp *server.MCPServer) {
	ListProvisioningRepositories.Register(mcp)
	CreateProvisioningRepositoryPR.Register(mcp)
	ManualSubmitGithubPullRequest.Register(mcp)
	ListProvisioningRepositoryBranches.Register(mcp)
	GetProvisioningRepository.Register(mcp)
	GetProvisioningRepositoryFileContent.Register(mcp)
	GetProvisioningRepositoryFileHistory.Register(mcp)
	ManageProvisioningRepositoryFile.Register(mcp)
	ManageFileDirectly.Register(mcp)
}

const listProvisioningRepositoriesToolPrompt = `List Git repositories configured for this Grafana instance. IMPORTANT: If any repositories are found, it means this Grafana instance IS managed by Git (GitOps). If no repositories are found, the instance is NOT Git-managed. Repositories are used for managing Grafana configuration as code (dashboards, datasources, etc.) through Git version control. Supports filtering by type (e.g., "github" for GitHub, "gitlab" for GitLab, "bitbucket" for Bitbucket, "local" for local repositories), uid (exact match), or name (regex pattern).`

var ListProvisioningRepositories = mcpgrafana.MustTool(
	"list_provisioning_repositories",
	listProvisioningRepositoriesToolPrompt,
	listProvisioningRepositories,
	mcp.WithTitleAnnotation("List Provisioning Repositories"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

const createProvisioningRepositoryPRToolPrompt = `**AUTO-CREATE PULL REQUEST** - Use this tool when the user wants you to automatically create and open a pull request for them on a managed provisioning repository. This tool will:
1. Automatically create the actual pull request via the backend API
2. Return the created PR details including URL and number
3. Handle all the technical work for the user without any manual steps

**Use this tool when the user says:**
- "Create a pull request for me"
- "Make a PR for this repository" 
- "Submit this as a pull request"
- "I want to create a pull request"
- "Open a pull request"
- Any request where they want YOU to automatically handle the PR creation

**This tool is ONLY for managed provisioning repositories** (repositories configured for GitOps in this instance).

**Do NOT use this tool for:**
- External GitHub repositories
- When user wants to manually review/edit PR details before submission
- Viewing existing pull requests

The tool requires: repository name, PR title, PR body/description, and the source branch reference.`

var CreateProvisioningRepositoryPR = mcpgrafana.MustTool(
	"create_provisioning_repository_pr",
	createProvisioningRepositoryPRToolPrompt,
	createProvisioningRepositoryPR,
	mcp.WithTitleAnnotation("Create Provisioning Repository Pull Request"),
)

const listProvisioningRepositoryBranchesToolPrompt = `List branches and refs within a specific Git repository configured for this Grafana instance. This tool shows all branches/refs available in the repository, including their commit hashes and reference URLs. Repository branches represent different versions of the configuration code (dashboards, datasources, etc.) managed through Git version control. Requires a repository_name parameter for exact matching, and optionally supports filtering by branch name using regex patterns.`

var ListProvisioningRepositoryBranches = mcpgrafana.MustTool(
	"list_provisioning_repository_branches",
	listProvisioningRepositoryBranchesToolPrompt,
	listProvisioningRepositoryBranches,
	mcp.WithTitleAnnotation("List Provisioning Repository Branches"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

const getProvisioningRepositoryToolPrompt = `Retrieves the details of a repository. Returns repository details including name, title, type, url, branch, and target. Use this to understand the details of a repository and determine where files are located. The URL indicates the Git repository source (e.g., GitHub, GitLab), the type specifies the repository format (e.g., "git"), the branch shows which branch contains the files, and the target path indicates the root directory within the repository where Grafana resources are stored.`

var GetProvisioningRepository = mcpgrafana.MustTool(
	"get_provisioning_repository",
	getProvisioningRepositoryToolPrompt,
	getProvisioningRepository,
	mcp.WithTitleAnnotation("Get Provisioning Repository Details"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

const getProvisioningRepositoryFileContentToolPrompt = `Retrieves the actual content of a specific file from a Git repository. Returns the complete file content as JSON, including metadata like file path, repository info, git hash, and source URL. This is useful for examining dashboard configurations, datasource settings, alerting rules, or any other Grafana resources stored in GitOps-managed repositories.`

var GetProvisioningRepositoryFileContent = mcpgrafana.MustTool(
	"get_provisioning_repository_file_content",
	getProvisioningRepositoryFileContentToolPrompt,
	getProvisioningRepositoryFileContent,
	mcp.WithTitleAnnotation("Get Provisioning Repository File Content"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

const getProvisioningRepositoryFileHistoryToolPrompt = `Retrieves the Git commit history for a specific file in a repository. Returns commit details including ref (branch/tag/commit), commit message, authors, and creation timestamp. Use this to understand when and how a file was modified, who made changes, and the evolution of configuration files like dashboards, datasources, or other Grafana resources.`

var GetProvisioningRepositoryFileHistory = mcpgrafana.MustTool(
	"get_provisioning_repository_file_history",
	getProvisioningRepositoryFileHistoryToolPrompt,
	getProvisioningRepositoryFileHistory,
	mcp.WithTitleAnnotation("Get Provisioning Repository File History"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

const manageProvisioningRepositoryFilePrompt = `Unified tool for managing files in Git repositories with automatic version control. Essential for GitOps workflows where Grafana configurations are managed as code.

IMPORTANT: This tool integrates with GitOps practices by automatically committing all changes to version control, enabling audit trails, rollback capabilities, and collaboration through code reviews.

Operations & Use Cases:
1. **Create**: Add new configuration files (dashboards, datasources, alerting rules, etc.)
2. **Update**: Modify existing configurations with new settings or parameters  
3. **Delete**: Remove deprecated or unused configuration files

The tool requires: repository name, file path, operation type (create/update/delete), optional content for create/update, commit message, and optional branch name.`

var ManageProvisioningRepositoryFile = mcpgrafana.MustTool(
	"manage_provisioning_repository_file",
	manageProvisioningRepositoryFilePrompt,
	manageProvisioningRepositoryFile,
	mcp.WithTitleAnnotation("Manage Repository File"),
	mcp.WithDestructiveHintAnnotation(true),
)

var ManageFileDirectly = mcpgrafana.MustTool(
	"manage_file",
	`Unified tool for managing files in Git repositories with automatic version control. Essential for GitOps workflows where Grafana configurations are managed as code.

	IMPORTANT: This tool is only possible to use for new dashboards, folders or existing provisioned ones.

	IMPORTANT: GitOps Workflow Integration
	This tool integrates with GitOps practices by automatically committing all changes to version control, enabling:
	- Audit trails for all configuration changes
	- Rollback capabilities to previous versions
	- Collaboration through pull requests and code reviews
	- Infrastructure-as-code principles

	Operations & Use Cases:
	1. **Create**: Add new configuration files (dashboards, datasources, alerting rules, etc.)
	2. **Update**: Modify existing configurations with new settings or parameters
	3. **Delete**: Remove deprecated or unused configuration files

	Common File Types:
	- Dashboard configurations (plain JSON format)
	- Datasource configurations (JSON format)
	- Alerting rules (JSON/YAML format)
	- Folder paths (with trailing slash)

	Content format: For dashboards, use the raw dashboard JSON (the GitOps controller handles Kubernetes resource wrapping).

	Example Dashboard File Content:
	{
	  "dashboard": {
	    "uid": "my-dashboard",
	    "title": "My Dashboard",
	    "panels": [/* panels */]
	  }
	}

	Best Practices:
	1. Use descriptive commit messages that explain the purpose of changes
	2. Validate configuration syntax before committing
	3. Use consistent naming conventions for files and directories
	4. Consider the impact of changes on existing dashboards

	Workflow Integration:
	1. Create or update configuration files using this tool
	2. Changes are automatically committed to the specified branch
	3. Provisioning / Git Sync controllers detect changes and apply them to Grafana
	4. Use version control history for troubleshooting and rollbacks

	Error Handling:
	- Validates file paths and repository access before operations
	- Provides detailed error messages for troubleshooting
	- Ensures atomic operations (all-or-nothing commits)
	- Handles concurrent modification conflicts gracefully

	Remember: All changes are immediately committed to version control, so ensure configurations are correct before submitting!`,
	manageProvisioningRepositoryFile,
	mcp.WithTitleAnnotation("Direct File Management for GitOps"),
	mcp.WithDestructiveHintAnnotation(true),
)

const manualSubmitGithubPullRequestToolPrompt = `**MANUAL GITHUB PR SUBMISSION** - Use this tool when the user wants to manually create a pull request for a managed provisioning repository. This tool will:
1. Look up the provisioning repository details to get the GitHub URL
2. Open GitHub's pull request creation page in their browser
3. Pre-fill the form with the provided title, body, and branch information
4. Let the user manually review, edit, and submit the PR themselves

**Use this tool when the user says:**
- "Open the GitHub PR page for this repository"
- "I want to manually create a PR for this provisioning repository"
- "Take me to GitHub to create a pull request for this repo"
- "Open the pull request page so I can review it before submitting"
- "I'll create the PR myself, just open the GitHub page"
- Any request where they want to do the final submission manually on GitHub

**This tool is ONLY for managed provisioning repositories** that are configured in this Grafana instance.

**Technical Details:**
- Validates that the repository exists in the provisioning configuration
- Extracts the GitHub URL from the repository configuration
- Opens GitHub's native pull request comparison/creation page
- Pre-fills the form but doesn't create the PR automatically
- User retains full control over the final submission

**Do NOT use this tool for:**
- When user wants automatic PR creation (use create_provisioning_repository_pr instead)
- External repositories not managed by this Grafana instance
- Non-GitHub provisioning repositories (GitLab, Bitbucket, etc.)
- Viewing existing pull requests

**Field Guidelines:**
- repository_name: Name of the provisioning repository (e.g., "dashboard-configs")
- title: Clear, descriptive PR title (e.g., "Add CPU monitoring dashboard")
- body: Detailed description of changes and their purpose
- base_branch: Target branch (usually "main" or "master")
- head_branch: Source branch name (e.g., "feature/add-dashboard")`

var ManualSubmitGithubPullRequest = mcpgrafana.MustTool(
	"manual_submit_github_pull_request",
	manualSubmitGithubPullRequestToolPrompt,
	manualSubmitGithubPullRequest,
	mcp.WithTitleAnnotation("Manual GitHub Pull Request Submission"),
)

type ListProvisioningRepositoriesParams struct {
	UID  string `json:"uid,omitempty" jsonschema:"description=Repository UID for exact matching"`
	Name string `json:"name,omitempty" jsonschema:"description=Repository name (can be a javascript regex pattern)"`
	Type string `json:"type,omitempty" jsonschema:"description=Filter repositories by type. Use this when the query implies a specific data modality. For example\\, use \"github\" for GitHub\\, \"gitlab\" for GitLab\\, \"bitbucket\" for Bitbucket\\, \"local\" for local repositories\\, etc."`
}

type CreateProvisioningRepositoryPRParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Name of the repository to create a pull request on (e.g. \"grafana\")"`
	Title          string `json:"title" jsonschema:"required,description=Title of the pull request (e.g. \"Add new feature\")"`
	Body           string `json:"body" jsonschema:"required,description=Body of the pull request (e.g. \"This is a new feature that I want to add to the project\")"`
	Ref            string `json:"ref" jsonschema:"required,description=Head branch of the pull request (e.g. \"feature/new-feature\")"`
}

type ListProvisioningRepositoryBranchesParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Repository name for exact matching"`
	BranchName     string `json:"branch_name,omitempty" jsonschema:"description=Branch name pattern (can be a javascript regex pattern)"`
}

type GetProvisioningRepositoryParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Repository name for exact matching"`
}

type GetProvisioningRepositoryFileContentParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Repository name for exact matching"`
	Path           string `json:"path" jsonschema:"required,description=Repository file path (e.g. \"dashboards/my-dashboard.json\")"`
	Ref            string `json:"ref,omitempty" jsonschema:"description=Git reference (branch\\, tag\\, or commit hash)"`
}

type GetProvisioningRepositoryFileHistoryParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Repository name for exact matching"`
	Path           string `json:"path" jsonschema:"required,description=Repository file path"`
	Ref            string `json:"ref,omitempty" jsonschema:"description=Git reference (branch\\, tag\\, or commit hash)"`
}

type ManageProvisioningRepositoryFileParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Repository name for exact matching"`
	Path           string `json:"path" jsonschema:"required,description=Repository file path relative to the repository root"`
	Branch         string `json:"branch,omitempty" jsonschema:"description=Git branch name for the operation. If not specified\\, changes will be pushed directly to the default branch"`
	Operation      string `json:"operation" jsonschema:"required,enum=create,enum=update,enum=delete,description=Operation to perform on the repository file"`
	Content        string `json:"content,omitempty" jsonschema:"description=File content for create or update operations. Required for create and update operations"`
	Message        string `json:"message" jsonschema:"required,description=Commit message describing the changes made"`
}

type Repository struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
	URL    string `json:"url"`
}

type RepositoryBranch struct {
	Name   string `json:"name"`
	Hash   string `json:"hash"`
	RefURL string `json:"refURL"`
}

type RepositoryFileHistory struct {
	Ref       string    `json:"ref"`
	Message   string    `json:"message"`
	Authors   []string  `json:"authors"`
	CreatedAt time.Time `json:"createdAt"`
}

type RepositoryDetail struct {
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
		Sync struct {
			Target string `json:"target"`
		} `json:"sync"`
	} `json:"spec"`
}

type FileContentResponse struct {
	Resource struct {
		File interface{} `json:"file"`
	} `json:"resource"`
	Ref  string `json:"ref"`
	Hash string `json:"hash"`
	URLs struct {
		SourceURL string `json:"sourceURL"`
	} `json:"urls"`
}

type ProvisioningResponse struct {
	Items []Repository `json:"items"`
}

type ProvisioningBranchesResponse struct {
	Items []RepositoryBranch `json:"items"`
}

type ProvisioningFileHistoryResponse struct {
	Items []RepositoryFileHistory `json:"items"`
}

type PullRequest struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Number int    `json:"number"`
}

type CreatePRResponse struct {
	PullRequest PullRequest `json:"pullRequest"`
}

type ManualSubmitGithubPullRequestParams struct {
	RepositoryName string `json:"repository_name" jsonschema:"required,description=Name of the provisioning repository to create a pull request for (e.g. \"dashboard-configs\")"`
	Title          string `json:"title" jsonschema:"required,description=Title of the pull request (e.g. \"Add new feature\")"`
	Body           string `json:"body" jsonschema:"required,description=Body of the pull request (e.g. \"This is a new feature that I want to add to the project\")"`
	BaseBranch     string `json:"base_branch" jsonschema:"required,description=Base branch of the pull request (e.g. \"main\")"`
	HeadBranch     string `json:"head_branch" jsonschema:"required,description=Head branch of the pull request (e.g. \"feature/new-feature\")"`
}

func formatRepository(r Repository) string {
	parts := []string{
		fmt.Sprintf("uid=%s", r.UID),
		fmt.Sprintf("name=%s", r.Name),
		fmt.Sprintf("type=%s", r.Type),
		fmt.Sprintf("target=%s", r.Target),
	}
	if r.URL != "" {
		parts = append(parts, fmt.Sprintf("url=%s", r.URL))
	}
	return fmt.Sprintf("- %s", strings.Join(parts, " | "))
}

func formatRepositoryBranch(b RepositoryBranch) string {
	shortHash := b.Hash
	if len(shortHash) > 8 {
		shortHash = shortHash[:8]
	}
	parts := []string{
		fmt.Sprintf("name=%s", b.Name),
		fmt.Sprintf("hash=%s", shortHash),
		fmt.Sprintf("refURL=%s", b.RefURL),
	}
	return fmt.Sprintf("- %s", strings.Join(parts, " | "))
}

func formatRepositoryFileHistory(h RepositoryFileHistory) string {
	parts := []string{
		fmt.Sprintf("ref=%s", h.Ref),
		fmt.Sprintf("message=%s", h.Message),
		fmt.Sprintf("authors=%s", strings.Join(h.Authors, ",")),
		fmt.Sprintf("createdAt=%s", h.CreatedAt.Format(time.RFC3339)),
	}
	return fmt.Sprintf("- %s", strings.Join(parts, " | "))
}

func formatRepositoryDetail(r RepositoryDetail) string {
	parts := []string{
		fmt.Sprintf("name=%s", r.Metadata.Name),
		fmt.Sprintf("title=%s", r.Spec.Title),
		fmt.Sprintf("type=%s", r.Spec.Type),
		fmt.Sprintf("url=%s", r.Spec.GitHub.URL),
		fmt.Sprintf("branch=%s", r.Spec.GitHub.Branch),
		fmt.Sprintf("target=%s", r.Spec.Sync.Target),
		fmt.Sprintf("path=%s", r.Spec.GitHub.Path),
	}
	return fmt.Sprintf("- %s", strings.Join(parts, " | "))
}

func createProvisioningRepositoryPR(ctx context.Context, args CreateProvisioningRepositoryPRParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Prepare URL parameters (matching TypeScript implementation)
	params := url.Values{}
	params.Set("title", args.Title)
	params.Set("content", args.Body)
	params.Set("ref", args.Ref)

	// Construct the API URL with query parameters
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s/pr?%s", args.RepositoryName, params.Encode())
	requestURL := fmt.Sprintf("%s%s", strings.TrimRight(cfg.URL, "/"), apiPath)

	// Create HTTP request with no body (parameters are in URL)
	req, err := http.NewRequestWithContext(ctx, "POST", requestURL, nil)
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
		return "", fmt.Errorf("repository '%s' not found or pull request creation not supported", args.RepositoryName)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response CreatePRResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// Use the PR URL directly from the response
	prURL := response.PullRequest.URL

	// Format the result with prominent URL
	result := fmt.Sprintf("Pull request created successfully!\n\n🔗 PR URL: %s\n\n📋 Details:\n- Title: %s\n- Number: %d\n- Repository: %s\n- Branch: %s",
		prURL,
		response.PullRequest.Title,
		response.PullRequest.Number,
		args.RepositoryName,
		args.Ref)

	return result, nil
}

func listProvisioningRepositories(ctx context.Context, args ListProvisioningRepositoriesParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Construct the API URL
	apiPath := "/apis/provisioning.grafana.app/v0alpha1/namespaces/default/settings"
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

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response ProvisioningResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// If no repositories found
	if len(response.Items) == 0 {
		return "No repositories found matching the criteria. This means the Grafana instance is NOT managed by Git.", nil
	}

	// Apply filters
	filtered := response.Items

	// Filter by UID (exact match)
	if args.UID != "" {
		var uidFiltered []Repository
		for _, r := range filtered {
			if r.UID == args.UID {
				uidFiltered = append(uidFiltered, r)
			}
		}
		filtered = uidFiltered
	}

	// Filter by type
	if args.Type != "" {
		var typeFiltered []Repository
		for _, r := range filtered {
			if r.Type == args.Type {
				typeFiltered = append(typeFiltered, r)
			}
		}
		filtered = typeFiltered
	}

	// Filter by name (regex)
	if args.Name != "" {
		var nameFiltered []Repository
		// Try to compile as regex first
		nameRegex, err := regexp.Compile("(?i)" + args.Name)
		if err != nil {
			// If regex compilation fails, use simple string matching
			for _, r := range filtered {
				if strings.Contains(strings.ToLower(r.Name), strings.ToLower(args.Name)) {
					nameFiltered = append(nameFiltered, r)
				}
			}
		} else {
			// Use regex matching
			for _, r := range filtered {
				if nameRegex.MatchString(r.Name) {
					nameFiltered = append(nameFiltered, r)
				}
			}
		}
		filtered = nameFiltered
	}

	// Check if filtering resulted in no matches
	if len(filtered) == 0 {
		return "No repositories found matching the criteria. This means the Grafana instance is NOT managed by Git.", nil
	}

	// Format the results
	countHeader := fmt.Sprintf("Found %d repositories. This means the Grafana instance IS managed by Git (GitOps):", len(filtered))

	var rows []string
	rows = append(rows, countHeader)

	for _, r := range filtered {
		rows = append(rows, formatRepository(r))
	}

	return strings.Join(rows, "\n"), nil
}

func listProvisioningRepositoryBranches(ctx context.Context, args ListProvisioningRepositoryBranchesParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Construct the API URL
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s/refs", args.RepositoryName)
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
	var response ProvisioningBranchesResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// If no branches found
	if len(response.Items) == 0 {
		return "Repository has no branches or refs", nil
	}

	// Apply branch name filter if provided
	filtered := response.Items

	if args.BranchName != "" {
		var branchFiltered []RepositoryBranch
		// Try to compile as regex first
		branchRegex, err := regexp.Compile("(?i)" + args.BranchName)
		if err != nil {
			// If regex compilation fails, use simple string matching
			for _, b := range filtered {
				if strings.Contains(strings.ToLower(b.Name), strings.ToLower(args.BranchName)) {
					branchFiltered = append(branchFiltered, b)
				}
			}
		} else {
			// Use regex matching
			for _, b := range filtered {
				if branchRegex.MatchString(b.Name) {
					branchFiltered = append(branchFiltered, b)
				}
			}
		}
		filtered = branchFiltered
	}

	// Check if filtering resulted in no matches
	if len(filtered) == 0 {
		return "No repository branches found matching the criteria.", nil
	}

	// Format the results
	countHeader := fmt.Sprintf("Found %d repository branches.", len(filtered))

	var rows []string
	rows = append(rows, countHeader)

	for _, b := range filtered {
		rows = append(rows, formatRepositoryBranch(b))
	}

	return strings.Join(rows, "\n"), nil
}

func getProvisioningRepository(ctx context.Context, args GetProvisioningRepositoryParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Construct the API URL
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s", args.RepositoryName)
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
		return fmt.Sprintf("Repository '%s' not found.", args.RepositoryName), nil
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response RepositoryDetail
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	return formatRepositoryDetail(response), nil
}

func getProvisioningRepositoryFileContent(ctx context.Context, args GetProvisioningRepositoryFileContentParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Encode the path for URL safety
	encodedPath := url.QueryEscape(args.Path)

	// Build the API path
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s/files/%s", args.RepositoryName, encodedPath)
	if args.Ref != "" {
		apiPath += fmt.Sprintf("?ref=%s", url.QueryEscape(args.Ref))
	}

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
		return fmt.Sprintf("File '%s' not found in repository '%s'.", args.Path, args.RepositoryName), nil
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response FileContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if response.Resource.File == nil {
		return fmt.Sprintf("No file content found for path: %s", args.Path), nil
	}

	// Format the JSON nicely for display
	fileContent, err := json.MarshalIndent(response.Resource.File, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling file content: %w", err)
	}

	// Add metadata about the file
	var metadata []string
	metadata = append(metadata, fmt.Sprintf("File: %s", args.Path))
	metadata = append(metadata, fmt.Sprintf("Repository: %s", args.RepositoryName))
	if response.Ref != "" {
		metadata = append(metadata, fmt.Sprintf("Ref: %s", response.Ref))
	}
	if response.Hash != "" {
		metadata = append(metadata, fmt.Sprintf("Hash: %s", response.Hash))
	}
	if response.URLs.SourceURL != "" {
		metadata = append(metadata, fmt.Sprintf("Source URL: %s", response.URLs.SourceURL))
	}
	metadata = append(metadata, "")
	metadata = append(metadata, "File Content:")
	metadata = append(metadata, "```json")
	metadata = append(metadata, string(fileContent))
	metadata = append(metadata, "```")

	return strings.Join(metadata, "\n"), nil
}

func getProvisioningRepositoryFileHistory(ctx context.Context, args GetProvisioningRepositoryFileHistoryParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Encode the path for URL safety
	encodedPath := url.QueryEscape(args.Path)

	// Build the API path
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s/history/%s", args.RepositoryName, encodedPath)
	if args.Ref != "" {
		apiPath += fmt.Sprintf("?ref=%s", url.QueryEscape(args.Ref))
	}

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
		return fmt.Sprintf("File '%s' not found in repository '%s' or no history available.", args.Path, args.RepositoryName), nil
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response ProvisioningFileHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if len(response.Items) == 0 {
		return "No history found for this file", nil
	}

	// Format the results
	countHeader := fmt.Sprintf("Found %d history entries.", len(response.Items))

	var rows []string
	rows = append(rows, countHeader)

	for _, h := range response.Items {
		rows = append(rows, formatRepositoryFileHistory(h))
	}

	return strings.Join(rows, "\n"), nil
}

func manageProvisioningRepositoryFile(ctx context.Context, args ManageProvisioningRepositoryFileParams) (string, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Encode the path for URL safety
	encodedPath := url.QueryEscape(args.Path)

	// Prepare query parameters
	params := url.Values{}
	params.Set("message", args.Message)
	if args.Branch != "" {
		params.Set("ref", args.Branch)
	}

	// Build the API path
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s/files/%s?%s", args.RepositoryName, encodedPath, params.Encode())

	url := fmt.Sprintf("%s%s", strings.TrimRight(cfg.URL, "/"), apiPath)

	var req *http.Request
	var err error

	switch args.Operation {
	case "create":
		req, err = http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(args.Content))
	case "update":
		req, err = http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(args.Content))
	case "delete":
		req, err = http.NewRequestWithContext(ctx, "DELETE", url, nil)
	default:
		return "", fmt.Errorf("invalid operation: %s. Must be create, update, or delete", args.Operation)
	}

	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	// Set content type for create/update operations
	if args.Operation == "create" || args.Operation == "update" {
		req.Header.Set("Content-Type", "application/json")
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
		return fmt.Sprintf("Repository '%s' not found or file operation not supported.", args.RepositoryName), fmt.Errorf("repository not found")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Format success message
	var operationPastTense string
	switch args.Operation {
	case "create":
		operationPastTense = "created"
	case "update":
		operationPastTense = "updated"
	case "delete":
		operationPastTense = "deleted"
	}

	result := fmt.Sprintf("File %s %s successfully", args.Path, operationPastTense)
	if args.Branch != "" {
		result += fmt.Sprintf(" on branch %s", args.Branch)
	}
	result += fmt.Sprintf("\n\nCommit message: %s", args.Message)

	return result, nil
}

// openURL opens the specified URL in the user's default browser
func openURL(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}

func manualSubmitGithubPullRequest(ctx context.Context, args ManualSubmitGithubPullRequestParams) (string, error) {
	// Validate that the repository exists in the provisioning configuration
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Construct the API URL
	apiPath := fmt.Sprintf("/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/%s", args.RepositoryName)
	requestURL := fmt.Sprintf("%s%s", strings.TrimRight(cfg.URL, "/"), apiPath)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
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
		return "", fmt.Errorf("repository '%s' not found", args.RepositoryName)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse the response
	var response RepositoryDetail
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	// Extract the GitHub URL from the repository configuration
	githubURL := response.Spec.GitHub.URL

	// Validate that this is a GitHub repository
	if !strings.Contains(strings.ToLower(githubURL), "github") {
		return "", fmt.Errorf("repository '%s' is not a GitHub repository (URL: %s)", args.RepositoryName, githubURL)
	}

	// Create URL parameters for GitHub's compare page
	params := url.Values{}
	params.Set("expand", "1")
	params.Set("title", args.Title)
	params.Set("body", args.Body)

	// Construct the GitHub compare URL
	prURL := fmt.Sprintf("%s/compare/%s...%s?%s",
		strings.TrimRight(githubURL, "/"),
		args.BaseBranch,
		args.HeadBranch,
		params.Encode())

	// Open the URL in the default browser
	if err := openURL(prURL); err != nil {
		return "", fmt.Errorf("failed to open browser: %w", err)
	}

	// Return success message with the URL
	result := fmt.Sprintf("GitHub pull request page opened in your browser!\n\n🔗 PR URL: %s\n\n📋 Pre-filled Details:\n- Repository: %s\n- Title: %s\n- Body: %s\n- Base Branch: %s\n- Head Branch: %s\n\nYou can now review and submit the pull request manually on GitHub.",
		prURL,
		args.RepositoryName,
		args.Title,
		args.Body,
		args.BaseBranch,
		args.HeadBranch)

	return result, nil
}
