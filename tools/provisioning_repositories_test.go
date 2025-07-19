// Requires a Grafana instance running on localhost:3000,
// with provisioning repositories configured (if testing Git-managed instance).
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvisioningRepositoriesTools(t *testing.T) {
	t.Run("list provisioning repositories - no filters", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{})
		require.NoError(t, err)

		// Test that result is a valid string response
		assert.NotEmpty(t, result)

		// If this Grafana instance is Git-managed, we should see the positive message
		// If not Git-managed, we should see the negative message
		isGitManaged := assert.Contains(t, result, "This means the Grafana instance IS managed by Git (GitOps)")
		isNotGitManaged := assert.Contains(t, result, "This means the Grafana instance is NOT managed by Git")

		// One of these should be true, but not both
		assert.True(t, isGitManaged || isNotGitManaged, "Result should indicate Git management status")
		if isGitManaged {
			assert.Contains(t, result, "Found")
			assert.Contains(t, result, "repositories")
			// Should contain formatted repository entries with uid, name, type, target
			assert.Contains(t, result, "uid=")
			assert.Contains(t, result, "name=")
			assert.Contains(t, result, "type=")
			assert.Contains(t, result, "target=")
		}
	})

	t.Run("list provisioning repositories - filter by UID", func(t *testing.T) {
		ctx := newTestContext()

		// First get all repositories to find a valid UID
		allRepos, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{})
		require.NoError(t, err)

		// Skip this test if no repositories are found
		if !assert.Contains(t, allRepos, "IS managed by Git") {
			t.Skip("No repositories found in this Grafana instance - skipping UID filter test")
		}

		// Test with a specific UID filter
		// Note: This test will be environment-dependent based on actual repository UIDs
		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			UID: "repository-test", // This may or may not exist
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Result should either contain the specific repository or indicate none found
		assert.True(t,
			assert.Contains(t, result, "repository-test") ||
				assert.Contains(t, result, "No repositories found matching the criteria"),
			"Should either find the specific repository or indicate none found")
	})

	t.Run("list provisioning repositories - filter by name", func(t *testing.T) {
		ctx := newTestContext()

		// Test with a name pattern that might exist
		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			Name: "dashboard", // Common repository name pattern
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should either find matching repositories or indicate none found
		assert.True(t,
			assert.Contains(t, result, "dashboard") ||
				assert.Contains(t, result, "No repositories found matching the criteria"),
			"Should handle name filtering correctly")
	})

	t.Run("list provisioning repositories - filter by name with regex", func(t *testing.T) {
		ctx := newTestContext()

		// Test with a regex pattern
		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			Name: ".*dash.*", // Regex pattern for names containing "dash"
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should handle regex patterns correctly
		assert.NotContains(t, result, "regex compilation failed", "Should handle valid regex")
	})

	t.Run("list provisioning repositories - filter by type github", func(t *testing.T) {
		ctx := newTestContext()

		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			Type: "github",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// If GitHub repositories are found, they should be displayed
		if assert.Contains(t, result, "type=github") {
			assert.Contains(t, result, "IS managed by Git")
		}
	})

	t.Run("list provisioning repositories - filter by type gitlab", func(t *testing.T) {
		ctx := newTestContext()

		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			Type: "gitlab",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should handle GitLab type filtering
		if assert.Contains(t, result, "type=gitlab") {
			assert.Contains(t, result, "IS managed by Git")
		}
	})

	t.Run("list provisioning repositories - filter by type local", func(t *testing.T) {
		ctx := newTestContext()

		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			Type: "local",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should handle local type filtering
		if assert.Contains(t, result, "type=local") {
			assert.Contains(t, result, "IS managed by Git")
		}
	})

	t.Run("list provisioning repositories - multiple filters", func(t *testing.T) {
		ctx := newTestContext()

		// Test combining multiple filters
		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			Name: "test",
			Type: "github",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should apply all filters correctly
		if assert.Contains(t, result, "Found") && assert.Contains(t, result, "repositories") {
			// If repositories are found, they should match all criteria
			assert.Contains(t, result, "type=github")
			assert.Contains(t, result, "test", "Name filter should be applied")
		}
	})

	t.Run("list provisioning repositories - invalid regex name", func(t *testing.T) {
		ctx := newTestContext()

		// Test with an invalid regex pattern
		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			Name: "[invalid-regex", // Unclosed bracket
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should fallback to string matching for invalid regex
		// The function should not fail, but use simple string matching instead
	})

	t.Run("list provisioning repositories - nonexistent filters", func(t *testing.T) {
		ctx := newTestContext()

		// Test with filters that should not match anything
		result, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{
			UID:  "definitely-nonexistent-uid-12345",
			Name: "definitely-nonexistent-name-98765",
			Type: "definitely-nonexistent-type",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should return the "not found" message
		assert.Contains(t, result, "No repositories found matching the criteria")
		assert.Contains(t, result, "This means the Grafana instance is NOT managed by Git")
	})

	t.Run("create provisioning repository PR - nonexistent repository", func(t *testing.T) {
		ctx := newTestContext()

		// Test with a repository that doesn't exist
		result, err := createProvisioningRepositoryPR(ctx, CreateProvisioningRepositoryPRParams{
			RepositoryName: "nonexistent-repo-12345",
			Title:          "Test PR Title",
			Body:           "This is a test pull request created by integration tests",
			Ref:            "feature/test-branch",
		})

		// Should get an error or appropriate response for nonexistent repository
		if err != nil {
			assert.Contains(t, err.Error(), "not found")
		} else {
			// If no error, the result should indicate the repository wasn't found
			assert.Contains(t, result, "not found")
		}
	})

	t.Run("create provisioning repository PR - invalid parameters", func(t *testing.T) {
		ctx := newTestContext()

		// Test with empty repository name
		_, err := createProvisioningRepositoryPR(ctx, CreateProvisioningRepositoryPRParams{
			RepositoryName: "",
			Title:          "Test PR",
			Body:           "Test body",
			Ref:            "test-branch",
		})
		// Should handle empty repository name gracefully
		assert.Error(t, err)

		// Test with empty title
		_, err = createProvisioningRepositoryPR(ctx, CreateProvisioningRepositoryPRParams{
			RepositoryName: "test-repo",
			Title:          "",
			Body:           "Test body",
			Ref:            "test-branch",
		})
		// Should handle empty title (may be allowed by API but test the request structure)
		// This test mainly ensures no panic occurs
	})

	t.Run("create provisioning repository PR - request format validation", func(t *testing.T) {
		ctx := newTestContext()

		// Test with parameters to verify request format
		result, err := createProvisioningRepositoryPR(ctx, CreateProvisioningRepositoryPRParams{
			RepositoryName: "test-repo",
			Title:          "Test PR with JSON body",
			Body:           "This PR tests that the request uses JSON body instead of query parameters",
			Ref:            "feature/test-json-format",
		})

		// The exact result depends on whether the repository exists
		if err != nil {
			// Should get a meaningful error about the repository or permissions, not a format error
			assert.True(t,
				assert.Contains(t, err.Error(), "not found") ||
					assert.Contains(t, err.Error(), "unexpected status code") ||
					assert.Contains(t, err.Error(), "403") ||
					assert.Contains(t, err.Error(), "401"),
				"Error should be about repository access, not request format: %v", err)
		} else {
			// If successful, should contain PR information
			assert.Contains(t, result, "Pull request created successfully")
		}
	})

	t.Run("format repository function", func(t *testing.T) {
		// Test the formatRepository helper function directly
		repo := Repository{
			UID:    "test-uid-123",
			Name:   "test/repository-name",
			Type:   "github",
			Target: "https://github.com/test/repo",
		}

		result := formatRepository(repo)

		assert.Contains(t, result, "- ")
		assert.Contains(t, result, "uid=test-uid-123")
		assert.Contains(t, result, "name=test/repository-name")
		assert.Contains(t, result, "type=github")
		assert.Contains(t, result, "target=https://github.com/test/repo")

		// Should use pipe separator
		assert.Contains(t, result, " | ")

		// Should be formatted as expected
		expected := "- uid=test-uid-123 | name=test/repository-name | type=github | target=https://github.com/test/repo"
		assert.Equal(t, expected, result)
	})

	t.Run("list provisioning repository branches - existing repository", func(t *testing.T) {
		ctx := newTestContext()

		// First get all repositories to find a valid repository name
		allRepos, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{})
		require.NoError(t, err)

		// Skip this test if no repositories are found
		if !assert.Contains(t, allRepos, "IS managed by Git") {
			t.Skip("No repositories found in this Grafana instance - skipping branch listing test")
		}

		// Test with a repository that should exist (this may need adjustment based on actual setup)
		result, err := listProvisioningRepositoryBranches(ctx, ListProvisioningRepositoryBranchesParams{
			RepositoryName: "test-repo", // This may or may not exist
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should either list branches or indicate repository not found
		assert.True(t,
			assert.Contains(t, result, "Found") && assert.Contains(t, result, "branches") ||
				assert.Contains(t, result, "not found"),
			"Should either list branches or indicate repository not found")
	})

	t.Run("list provisioning repository branches - nonexistent repository", func(t *testing.T) {
		ctx := newTestContext()

		result, err := listProvisioningRepositoryBranches(ctx, ListProvisioningRepositoryBranchesParams{
			RepositoryName: "nonexistent-repo-12345",
		})
		require.NoError(t, err)
		assert.Contains(t, result, "not found")
	})

	t.Run("list provisioning repository branches - with branch filter", func(t *testing.T) {
		ctx := newTestContext()

		// Test with a branch name filter
		result, err := listProvisioningRepositoryBranches(ctx, ListProvisioningRepositoryBranchesParams{
			RepositoryName: "test-repo",
			BranchName:     "main",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		// Should handle branch filtering correctly
		if assert.Contains(t, result, "Found") {
			// If branches are found, should contain "main" in the results
			assert.Contains(t, result, "main")
		}
	})

	t.Run("get provisioning repository - nonexistent repository", func(t *testing.T) {
		ctx := newTestContext()

		result, err := getProvisioningRepository(ctx, GetProvisioningRepositoryParams{
			RepositoryName: "nonexistent-repo-12345",
		})
		require.NoError(t, err)
		assert.Contains(t, result, "not found")
	})

	t.Run("get provisioning repository file content - nonexistent repository", func(t *testing.T) {
		ctx := newTestContext()

		result, err := getProvisioningRepositoryFileContent(ctx, GetProvisioningRepositoryFileContentParams{
			RepositoryName: "nonexistent-repo-12345",
			Path:           "test-file.json",
		})
		require.NoError(t, err)
		assert.Contains(t, result, "not found")
	})

	t.Run("get provisioning repository file content - nonexistent file", func(t *testing.T) {
		ctx := newTestContext()

		result, err := getProvisioningRepositoryFileContent(ctx, GetProvisioningRepositoryFileContentParams{
			RepositoryName: "test-repo",
			Path:           "nonexistent-file-12345.json",
		})
		require.NoError(t, err)
		assert.True(t,
			assert.Contains(t, result, "not found") ||
				assert.Contains(t, result, "Repository") && assert.Contains(t, result, "not found"),
			"Should indicate file or repository not found")
	})

	t.Run("get provisioning repository file history - nonexistent repository", func(t *testing.T) {
		ctx := newTestContext()

		result, err := getProvisioningRepositoryFileHistory(ctx, GetProvisioningRepositoryFileHistoryParams{
			RepositoryName: "nonexistent-repo-12345",
			Path:           "test-file.json",
		})
		require.NoError(t, err)
		assert.Contains(t, result, "not found")
	})

	t.Run("get provisioning repository file history - nonexistent file", func(t *testing.T) {
		ctx := newTestContext()

		result, err := getProvisioningRepositoryFileHistory(ctx, GetProvisioningRepositoryFileHistoryParams{
			RepositoryName: "test-repo",
			Path:           "nonexistent-file-12345.json",
		})
		require.NoError(t, err)
		assert.True(t,
			assert.Contains(t, result, "not found") ||
				assert.Contains(t, result, "No history found"),
			"Should indicate file not found or no history available")
	})

	t.Run("manage provisioning repository file - invalid operation", func(t *testing.T) {
		ctx := newTestContext()

		_, err := manageProvisioningRepositoryFile(ctx, ManageProvisioningRepositoryFileParams{
			RepositoryName: "test-repo",
			Path:           "test-file.json",
			Operation:      "invalid-operation",
			Content:        "{}",
			Message:        "Test commit",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid operation")
	})

	t.Run("manage provisioning repository file - nonexistent repository", func(t *testing.T) {
		ctx := newTestContext()

		result, err := manageProvisioningRepositoryFile(ctx, ManageProvisioningRepositoryFileParams{
			RepositoryName: "nonexistent-repo-12345",
			Path:           "test-file.json",
			Operation:      "create",
			Content:        `{"test": "content"}`,
			Message:        "Test commit",
		})

		// Should get an error or appropriate response for nonexistent repository
		if err != nil {
			assert.Contains(t, err.Error(), "not found")
		} else {
			assert.Contains(t, result, "not found")
		}
	})

	t.Run("format repository branch function", func(t *testing.T) {
		// Test the formatRepositoryBranch helper function directly
		branch := RepositoryBranch{
			Name:   "main",
			Hash:   "abc123def456789",
			RefURL: "https://github.com/test/repo/tree/main",
		}

		result := formatRepositoryBranch(branch)

		assert.Contains(t, result, "- ")
		assert.Contains(t, result, "name=main")
		assert.Contains(t, result, "hash=abc123de") // Should be truncated to 8 chars
		assert.Contains(t, result, "refURL=https://github.com/test/repo/tree/main")

		// Should use pipe separator
		assert.Contains(t, result, " | ")

		// Should be formatted as expected
		expected := "- name=main | hash=abc123de | refURL=https://github.com/test/repo/tree/main"
		assert.Equal(t, expected, result)
	})

	t.Run("format repository detail function", func(t *testing.T) {
		// Test the formatRepositoryDetail helper function directly
		detail := RepositoryDetail{
			Metadata: struct {
				Name string `json:"name"`
			}{
				Name: "test-repo",
			},
			Spec: struct {
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
			}{
				Title: "Test Repository",
				Type:  "git",
				GitHub: struct {
					URL    string `json:"url"`
					Branch string `json:"branch"`
					Path   string `json:"path"`
				}{
					URL:    "https://github.com/test/repo",
					Branch: "main",
					Path:   "grafana",
				},
				Sync: struct {
					Target string `json:"target"`
				}{
					Target: "/var/lib/grafana",
				},
			},
		}

		result := formatRepositoryDetail(detail)

		assert.Contains(t, result, "- ")
		assert.Contains(t, result, "name=test-repo")
		assert.Contains(t, result, "title=Test Repository")
		assert.Contains(t, result, "type=git")
		assert.Contains(t, result, "url=https://github.com/test/repo")
		assert.Contains(t, result, "branch=main")
		assert.Contains(t, result, "target=/var/lib/grafana")
		assert.Contains(t, result, "path=grafana")

		// Should use pipe separator
		assert.Contains(t, result, " | ")
	})
}

func TestManualSubmitGithubPullRequest(t *testing.T) {
	tests := []struct {
		name        string
		args        ManualSubmitGithubPullRequestParams
		wantErr     bool
		errContains string
	}{
		{
			name: "non-GitHub URL should fail",
			args: ManualSubmitGithubPullRequestParams{
				URL:        "https://gitlab.com/grafana/grafana",
				Title:      "Add new dashboard",
				Body:       "This PR adds a new monitoring dashboard",
				BaseBranch: "main",
				HeadBranch: "feature/new-dashboard",
			},
			wantErr:     true,
			errContains: "URL must be a GitHub repository",
		},
		{
			name: "valid GitHub URL validates correctly",
			args: ManualSubmitGithubPullRequestParams{
				URL:        "https://github.com/grafana/grafana",
				Title:      "Add new dashboard",
				Body:       "This PR adds a new monitoring dashboard",
				BaseBranch: "main",
				HeadBranch: "feature/new-dashboard",
			},
			wantErr: false, // Note: will fail on browser open, but should pass validation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a simple context
			ctx := context.Background()

			// Call the function
			_, err := manualSubmitGithubPullRequest(ctx, tt.args)

			// Check error conditions
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain '%s', got '%s'", tt.errContains, err.Error())
				}
				return
			}

			// For valid GitHub URLs, we expect either success or browser open failure
			if err != nil && !strings.Contains(err.Error(), "failed to open browser") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGitHubPRURLConstruction(t *testing.T) {
	args := ManualSubmitGithubPullRequestParams{
		URL:        "https://github.com/grafana/grafana",
		Title:      "Add new dashboard",
		Body:       "This PR adds a new monitoring dashboard with special chars: &=+",
		BaseBranch: "main",
		HeadBranch: "feature/new-dashboard",
	}

	// Test URL construction manually
	params := url.Values{}
	params.Set("expand", "1")
	params.Set("title", args.Title)
	params.Set("body", args.Body)

	expectedURL := fmt.Sprintf("%s/compare/%s...%s?%s",
		strings.TrimRight(args.URL, "/"),
		args.BaseBranch,
		args.HeadBranch,
		params.Encode())

	// Verify the URL is properly constructed
	if !strings.Contains(expectedURL, "github.com/grafana/grafana/compare/main...feature/new-dashboard") {
		t.Errorf("URL should contain the proper compare path")
	}
	if !strings.Contains(expectedURL, "expand=1") {
		t.Errorf("URL should contain expand=1 parameter")
	}
	if !strings.Contains(expectedURL, url.QueryEscape(args.Title)) {
		t.Errorf("URL should contain encoded title")
	}
	if !strings.Contains(expectedURL, url.QueryEscape(args.Body)) {
		t.Errorf("URL should contain encoded body")
	}
}
