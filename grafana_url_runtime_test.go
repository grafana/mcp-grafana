package mcpgrafana

import (
	"context"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetGrafanaURL(t *testing.T) {
	runtimeURL := NewRuntimeGrafanaURL()
	token := "service-account-token"

	result, err := setGrafanaURL(runtimeURL, SetGrafanaURLParams{
		URL:   "tesco-6",
		Token: &token,
	})
	require.NoError(t, err)
	assert.Equal(t, "http://tesco-6/grafana", result.URL)
	assert.Equal(t, "http://tesco-6/grafana", func() string {
		url, _, _ := runtimeURL.get()
		return url
	}())
	_, storedToken, ok := runtimeURL.get()
	assert.True(t, ok)
	assert.Equal(t, token, storedToken)

	_, err = setGrafanaURL(runtimeURL, SetGrafanaURLParams{URL: "ftp://grafana.example.com"})
	assert.ErrorIs(t, err, ErrInvalidGrafanaURL)
}

func TestNormalizeSetGrafanaURL(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"tesco-6", "http://tesco-6/grafana"},
		{"https://tesco-6/", "http://tesco-6/grafana"},
		{"http://tesco-6", "http://tesco-6/grafana"},
		{"http://tesco-6/grafana", "http://tesco-6/grafana"},
		{"https://grafana.example.com/", "https://grafana.example.com"},
		{"localhost:3000", "http://localhost:3000"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeSetGrafanaURL(tc.input))
		})
	}
}

func TestSetGrafanaURLOmittedTokenClearsToken(t *testing.T) {
	runtimeURL := NewRuntimeGrafanaURL()
	runtimeURL.Set("https://old.example.com", "old-token")

	_, err := setGrafanaURL(runtimeURL, SetGrafanaURLParams{URL: "https://new.example.com"})
	require.NoError(t, err)

	url, token, ok := runtimeURL.get()
	assert.True(t, ok)
	assert.Equal(t, "https://new.example.com", url)
	assert.Empty(t, token)
}

func TestRuntimeGrafanaURLMiddlewareAppliesSelectedConnection(t *testing.T) {
	runtimeURL := NewRuntimeGrafanaURL()
	runtimeURL.Set("https://grafana.example.com", "service-account-token")

	ctx := WithGrafanaConfig(context.Background(), GrafanaConfig{
		URL:          "https://configured.example.com",
		APIKey:       "configured-token",
		BasicAuth:    url.UserPassword("configured-user", "configured-password"),
		AccessToken:  "configured-access-token",
		IDToken:      "configured-id-token",
		ExtraHeaders: map[string]string{"X-Configured-Header": "configured-value"},
	})
	handler := RuntimeGrafanaURLMiddleware(runtimeURL)(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		config := GrafanaConfigFromContext(ctx)
		assert.Equal(t, "https://grafana.example.com", config.URL)
		assert.Equal(t, "service-account-token", config.APIKey)
		assert.Nil(t, config.BasicAuth)
		assert.Empty(t, config.AccessToken)
		assert.Empty(t, config.IDToken)
		assert.Empty(t, config.ExtraHeaders)
		assert.Equal(t, "https://grafana.example.com", config.OverrideURL)
		assert.NotNil(t, GrafanaClientFromContext(ctx))
		assert.NotNil(t, KubernetesClientFromContext(ctx))
		assert.NotNil(t, IncidentClientFromContext(ctx))
		return NewToolResultText("ok"), nil
	})

	_, err := handler(ctx, "tools/call", &mcp.CallToolRequest{})
	require.NoError(t, err)
}

func TestRuntimeGrafanaURLMiddlewareRequiresTarget(t *testing.T) {
	runtimeURL := NewRuntimeGrafanaURL()
	called := false
	handler := RuntimeGrafanaURLMiddleware(runtimeURL)(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		called = true
		return NewToolResultText("ok"), nil
	})

	result, err := handler(context.Background(), "tools/call", &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "user_info"},
	})
	require.NoError(t, err)
	assert.False(t, called)
	toolResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)
	assert.True(t, toolResult.IsError)
	require.Len(t, toolResult.Content, 1)
	assert.Contains(t, toolResult.Content[0].(*mcp.TextContent).Text, "set_grafana_url")
}

func TestRuntimeGrafanaURLMiddlewareAllowsExemptToolsAndConfiguredTargets(t *testing.T) {
	cases := append([]string{
		"search_docs",
		"get_doc",
		"suggest_loki_alloy_label_config",
		"get_query_examples",
		"set_grafana_url",
	}, "user_info")

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			runtimeURL := NewRuntimeGrafanaURL()
			if name == "user_info" {
				runtimeURL = NewRuntimeGrafanaURL(true)
			}
			handler := RuntimeGrafanaURLMiddleware(runtimeURL)(func(context.Context, string, mcp.Request) (mcp.Result, error) {
				called = true
				return NewToolResultText("ok"), nil
			})

			_, err := handler(context.Background(), "tools/call", &mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{Name: name},
			})
			require.NoError(t, err)
			assert.True(t, called)
		})
	}
}

func TestSetGrafanaURLToolIsNotOrgScoped(t *testing.T) {
	DynamicMultiOrgEnabled = true
	t.Cleanup(func() { DynamicMultiOrgEnabled = false })

	tool := NewSetGrafanaURLTool(NewRuntimeGrafanaURL())
	assert.NotContains(t, schemaProperties(t, tool.resolveTool()), OrgIDArgument)
	assert.Equal(t, "Connect to Grafana store", tool.Tool.Annotations.Title)
	assert.Contains(t, tool.Tool.Description, "Call this first")
	assert.Contains(t, tool.Tool.Description, "tesco-6")
}
