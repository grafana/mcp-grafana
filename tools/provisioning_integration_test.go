// Requires the docker-compose environment (Grafana 13.x with the provisioning
// feature toggle, a gitserver serving test-repo, and the provisioning-repo-seed
// job registering it). Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"encoding/base64"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These match testdata/provisioning-repo-seed.sh and the seeded git repo.
const (
	testProvisioningRepo   = "test-repo"
	testProvisioningBranch = "feature/extra-dashboard"
)

func TestListProvisioningRepositories_Integration(t *testing.T) {
	ctx := newTestContext()

	repos, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{})
	require.NoError(t, err)

	var repo *ProvisioningRepository
	for i := range repos {
		if repos[i].Name == testProvisioningRepo {
			repo = &repos[i]
			break
		}
	}
	require.NotNil(t, repo, "expected the seeded %q repository to be listed", testProvisioningRepo)

	assert.Equal(t, "git", repo.Type)
	assert.Equal(t, "http://gitserver/git/test-repo.git", repo.URL)
	assert.Equal(t, "main", repo.Branch)
	assert.True(t, repo.Healthy, "seeded repository should be healthy")
}

func TestValidateProvisioningFile_Integration(t *testing.T) {
	ctx := newTestContext()

	t.Run("valid dashboard on main", func(t *testing.T) {
		out, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{
			Repo: testProvisioningRepo,
			Path: "sample-dashboard.json",
		})
		require.NoError(t, err)
		assert.True(t, out.Valid, "sample dashboard should validate")
		assert.Empty(t, out.Errors)
		require.NotNil(t, out.Type)
		assert.Equal(t, "Dashboard", out.Type.Kind)
	})

	t.Run("valid dashboard on feature branch", func(t *testing.T) {
		out, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{
			Repo: testProvisioningRepo,
			Path: "extra-dashboard.json",
			Ref:  testProvisioningBranch,
		})
		require.NoError(t, err)
		assert.True(t, out.Valid, "extra dashboard on feature branch should validate")
		assert.Empty(t, out.Errors)
		require.NotNil(t, out.Type)
		assert.Equal(t, "Dashboard", out.Type.Kind)
	})

	t.Run("invalid dashboard on feature branch", func(t *testing.T) {
		out, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{
			Repo: testProvisioningRepo,
			Path: "broken-dashboard.json",
			Ref:  testProvisioningBranch,
		})
		require.NoError(t, err)
		assert.False(t, out.Valid, "broken dashboard should fail validation")
		require.NotEmpty(t, out.Errors, "expected structured validation errors")

		var sawVariablesError bool
		for _, e := range out.Errors {
			assert.NotEmpty(t, e.Message)
			if e.Field == "DashboardSpec.variables" || e.Field == "DashboardSpec.variables.0.kind" {
				sawVariablesError = true
			}
		}
		assert.True(t, sawVariablesError, "expected an error on DashboardSpec.variables, got %+v", out.Errors)
	})
}

func TestRenderProvisioningPreview_Integration(t *testing.T) {
	ctx := newTestContext()

	cases := []struct {
		name string
		path string
		ref  string
	}{
		{"sample dashboard on main", "sample-dashboard.json", ""},
		{"extra dashboard on feature branch", "extra-dashboard.json", testProvisioningBranch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := getPanelImage(ctx, GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: testProvisioningRepo,
					Path: tc.path,
					Ref:  tc.ref,
				},
			})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.IsError, "render returned an error result: %+v", result.Content)
			require.Len(t, result.Content, 1)

			img, ok := result.Content[0].(mcp.ImageContent)
			require.True(t, ok, "expected ImageContent, got %T", result.Content[0])
			assert.Equal(t, "image/png", img.MIMEType)

			data, err := base64.StdEncoding.DecodeString(img.Data)
			require.NoError(t, err)
			require.NotEmpty(t, data, "rendered PNG should not be empty")
			// PNG magic number.
			assert.Equal(t, []byte{0x89, 'P', 'N', 'G'}, data[:4], "rendered image should be a PNG")
		})
	}
}
