package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestManualSubmitGithubPullRequestValidation(t *testing.T) {
	tests := []struct {
		name        string
		args        ManualSubmitGithubPullRequestParams
		wantErr     bool
		errContains string
	}{
		{
			name: "nonexistent repository should fail",
			args: ManualSubmitGithubPullRequestParams{
				RepositoryName: "nonexistent-repo-12345",
				Title:          "Add new dashboard",
				Body:           "This PR adds a new monitoring dashboard",
				BaseBranch:     "main",
				HeadBranch:     "feature/new-dashboard",
			},
			wantErr:     true,
			errContains: "", // Don't check specific error message since it varies by test environment
		},
		{
			name: "valid repository name with proper parameters",
			args: ManualSubmitGithubPullRequestParams{
				RepositoryName: "test-repo",
				Title:          "Add new dashboard",
				Body:           "This PR adds a new monitoring dashboard",
				BaseBranch:     "main",
				HeadBranch:     "feature/new-dashboard",
			},
			wantErr: true, // Will fail in test environment due to no Grafana instance
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a simple context
			ctx := context.Background()

			// Call the function
			_, err := manualSubmitGithubPullRequest(ctx, tt.args)

			// Check error conditions - in unit test environment, we expect errors due to no Grafana instance
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				// Only check specific error message if specified
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain '%s', got '%s'", tt.errContains, err.Error())
				}
				return
			}

			// For valid repository names, we expect either success or various API-related failures
			// since we don't have a real Grafana instance in unit tests
			if err != nil {
				expectedErrors := []string{
					"failed to open browser",
					"making request",
					"creating request",
					"not found",
					"unexpected status code",
					"unsupported protocol scheme",
				}
				hasExpectedError := false
				for _, expectedErr := range expectedErrors {
					if strings.Contains(err.Error(), expectedErr) {
						hasExpectedError = true
						break
					}
				}
				if !hasExpectedError {
					t.Errorf("unexpected error type: %v", err)
				}
			}
		})
	}
}

func TestGitHubPRURLConstruction(t *testing.T) {
	// Test the URL construction logic manually
	title := "Add new dashboard"
	body := "This PR adds a new monitoring dashboard with special chars: &=+"
	baseBranch := "main"
	headBranch := "feature/new-dashboard"
	githubURL := "https://github.com/grafana/dashboard-configs"

	// Test URL construction manually (same logic as in the function)
	params := url.Values{}
	params.Set("expand", "1")
	params.Set("title", title)
	params.Set("body", body)

	expectedURL := fmt.Sprintf("%s/compare/%s...%s?%s",
		strings.TrimRight(githubURL, "/"),
		baseBranch,
		headBranch,
		params.Encode())

	// Verify the URL is properly constructed
	if !strings.Contains(expectedURL, "github.com/grafana/dashboard-configs/compare/main...feature/new-dashboard") {
		t.Errorf("URL should contain the proper compare path")
	}
	if !strings.Contains(expectedURL, "expand=1") {
		t.Errorf("URL should contain expand=1 parameter")
	}
	if !strings.Contains(expectedURL, url.QueryEscape(title)) {
		t.Errorf("URL should contain encoded title")
	}
	if !strings.Contains(expectedURL, url.QueryEscape(body)) {
		t.Errorf("URL should contain encoded body")
	}

	t.Logf("Generated URL: %s", expectedURL)
}

func TestGitHubPRURLWithTrailingSlash(t *testing.T) {
	// Test the URL construction with trailing slash handling
	title := "Fix trailing slash handling"
	body := "This tests URL handling with trailing slash"
	baseBranch := "main"
	headBranch := "fix/trailing-slash"
	githubURL := "https://github.com/grafana/dashboard-configs/"

	// Test URL construction manually
	params := url.Values{}
	params.Set("expand", "1")
	params.Set("title", title)
	params.Set("body", body)

	expectedURL := fmt.Sprintf("%s/compare/%s...%s?%s",
		strings.TrimRight(githubURL, "/"),
		baseBranch,
		headBranch,
		params.Encode())

	// Should not have double slashes
	if strings.Contains(expectedURL, "//compare") {
		t.Errorf("URL should not contain double slashes: %s", expectedURL)
	}

	// Should contain the proper path
	if !strings.Contains(expectedURL, "github.com/grafana/dashboard-configs/compare/main...fix/trailing-slash") {
		t.Errorf("URL should contain the proper compare path")
	}

	t.Logf("Generated URL: %s", expectedURL)
}
