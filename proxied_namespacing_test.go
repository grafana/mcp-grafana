package mcpgrafana

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yosida95/uritemplate/v3"
)

func TestNamespaceResourceURI_Roundtrip(t *testing.T) {
	cases := []struct {
		name        string
		dsType      string
		uid         string
		originalURI string
	}{
		{"simple", "tempo", "abc-123", "docs://traceql/basic"},
		{"with-query", "tempo", "abc-123", "docs://traceql/metrics?v=1&q=foo"},
		{"with-colons", "tempo", "abc-123", "urn:something:with:colons"},
		{"uid-with-dashes-underscores", "tempo", "ds_abc-123", "docs://x"},
		{"empty-original", "tempo", "abc", ""},
		{"with-plus", "tempo", "abc-123", "docs://a+b/foo"},
		{"with-space", "tempo", "abc-123", "docs://a b/foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := namespaceResourceURI(tc.dsType, tc.uid, tc.originalURI)
			gotType, gotUID, gotURI, err := parseNamespacedResourceURI(ns)
			require.NoError(t, err)
			assert.Equal(t, tc.dsType, gotType)
			assert.Equal(t, tc.uid, gotUID)
			assert.Equal(t, tc.originalURI, gotURI)
		})
	}
}

func TestParseNamespacedResourceURI_Errors(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"wrong-scheme", "https://example.com/foo"},
		{"missing-parts", "urn:mcp-grafana:tempo"},
		{"only-prefix", "urn:mcp-grafana:"},
		{"bad-encoding", "urn:mcp-grafana:tempo:abc:%ZZ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := parseNamespacedResourceURI(tc.uri)
			assert.Error(t, err)
		})
	}
}

func TestNamespacePromptName_Roundtrip(t *testing.T) {
	cases := []struct {
		name         string
		dsType       string
		originalName string
	}{
		{"simple", "tempo", "summarize-trace"},
		{"name-with-underscore", "tempo", "deep_dive_query"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := namespacePromptName(tc.dsType, tc.originalName)
			gotType, gotName, err := parseProxiedPromptName(ns)
			require.NoError(t, err)
			assert.Equal(t, tc.dsType, gotType)
			assert.Equal(t, tc.originalName, gotName)
		})
	}
}

func TestParseProxiedPromptName_Error(t *testing.T) {
	_, _, err := parseProxiedPromptName("nounderscore")
	assert.Error(t, err)
}

func TestNamespacePrompt_AddsDatasourceUidArgument(t *testing.T) {
	client := &ProxiedClient{
		DatasourceUID:  "abc-123",
		DatasourceName: "Prod Tempo",
		DatasourceType: "tempo",
	}
	in := mcp.Prompt{
		Name: "trace-summary",
		Arguments: []mcp.PromptArgument{
			{Name: "trace_id", Required: true},
		},
	}
	out := namespacePrompt(in, client)
	assert.Equal(t, "tempo_trace-summary", out.Name)
	require.Len(t, out.Arguments, 2)
	assert.Equal(t, "datasourceUid", out.Arguments[0].Name)
	assert.True(t, out.Arguments[0].Required)
	assert.Equal(t, "trace_id", out.Arguments[1].Name)
}

func TestNamespaceResource_RewritesURIAndName(t *testing.T) {
	client := &ProxiedClient{
		DatasourceUID:  "abc-123",
		DatasourceName: "Prod Tempo",
		DatasourceType: "tempo",
	}
	in := mcp.Resource{
		URI:         "docs://traceql/basic",
		Name:        "TraceQL Basics",
		Description: "...",
		MIMEType:    "text/markdown",
	}
	out := namespaceResource(in, client)

	gotType, gotUID, gotURI, err := parseNamespacedResourceURI(out.URI)
	require.NoError(t, err)
	assert.Equal(t, "tempo", gotType)
	assert.Equal(t, "abc-123", gotUID)
	assert.Equal(t, "docs://traceql/basic", gotURI)
	assert.Contains(t, out.Name, "TraceQL Basics")
	assert.Contains(t, out.Name, "Prod Tempo")
	assert.Equal(t, in.MIMEType, out.MIMEType)
}

// TestParseNamespacedResourceURI_TemplateExpansionPreservesPlus verifies that
// a URI produced by expanding a verbatim-embedded resource template (which
// bypasses PathEscape) round-trips correctly through parseNamespacedResourceURI
// when the upstream URI contains a literal '+'.
func TestParseNamespacedResourceURI_TemplateExpansionPreservesPlus(t *testing.T) {
	// Simulate what a client sends back after expanding a template like
	// urn:mcp-grafana:tempo:abc-123:docs://a+b/{section} with section=foo.
	expanded := "urn:mcp-grafana:tempo:abc-123:docs://a+b/foo"
	gotType, gotUID, gotURI, err := parseNamespacedResourceURI(expanded)
	require.NoError(t, err)
	assert.Equal(t, "tempo", gotType)
	assert.Equal(t, "abc-123", gotUID)
	assert.Equal(t, "docs://a+b/foo", gotURI)
}

func TestNamespaceResourceTemplate_RewritesURIAndName(t *testing.T) {
	client := &ProxiedClient{
		DatasourceUID:  "abc-123",
		DatasourceName: "Prod Tempo",
		DatasourceType: "tempo",
	}
	tmpl, err := uritemplate.New("docs://traceql/{section}")
	require.NoError(t, err)

	in := mcp.ResourceTemplate{
		URITemplate: &mcp.URITemplate{Template: tmpl},
		Name:        "TraceQL Section",
		MIMEType:    "text/markdown",
	}
	out, ok := namespaceResourceTemplate(in, client)
	require.True(t, ok)
	require.NotNil(t, out.URITemplate)
	rawOut := out.URITemplate.Raw()
	assert.Contains(t, rawOut, "urn:mcp-grafana:tempo:abc-123:")
	assert.Contains(t, rawOut, "{section}")
	assert.Contains(t, out.Name, "TraceQL Section")
	assert.Contains(t, out.Name, "Prod Tempo")
}

func TestNamespaceResourceTemplate_NilURITemplateSkipped(t *testing.T) {
	client := &ProxiedClient{
		DatasourceUID:  "abc-123",
		DatasourceName: "Prod Tempo",
		DatasourceType: "tempo",
	}
	in := mcp.ResourceTemplate{
		URITemplate: nil,
		Name:        "Broken",
	}
	_, ok := namespaceResourceTemplate(in, client)
	assert.False(t, ok)
}
