//go:build unit

package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func intPtr(i int) *int {
	return &i
}

func TestStringOrSlice_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected StringOrSlice
		wantErr  bool
	}{
		{
			name:     "Single string value",
			input:    `"prometheus"`,
			expected: StringOrSlice{"prometheus"},
		},
		{
			name:     "Array with single value",
			input:    `["prometheus"]`,
			expected: StringOrSlice{"prometheus"},
		},
		{
			name:     "Array with multiple values",
			input:    `["server1", "server2", "server3"]`,
			expected: StringOrSlice{"server1", "server2", "server3"},
		},
		{
			name:     "Empty array",
			input:    `[]`,
			expected: StringOrSlice{},
		},
		{
			name:    "Invalid type (number)",
			input:   `42`,
			wantErr: true,
		},
		{
			name:    "Invalid type (object)",
			input:   `{"key": "value"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result StringOrSlice
			err := json.Unmarshal([]byte(tt.input), &result)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStringOrSlice_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    StringOrSlice
		expected string
	}{
		{
			name:     "Single value marshals as string",
			input:    StringOrSlice{"prometheus"},
			expected: `"prometheus"`,
		},
		{
			name:     "Multiple values marshal as array",
			input:    StringOrSlice{"server1", "server2"},
			expected: `["server1","server2"]`,
		},
		{
			name:     "Empty slice marshals as empty array",
			input:    StringOrSlice{},
			expected: `[]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := json.Marshal(tt.input)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(result))
		})
	}
}

func TestGetPanelImageParams_UnmarshalVariables(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]StringOrSlice
	}{
		{
			name:  "Single string values (backward compatible)",
			input: `{"dashboardUid": "abc", "variables": {"var-datasource": "prometheus", "var-host": "server01"}}`,
			expected: map[string]StringOrSlice{
				"var-datasource": {"prometheus"},
				"var-host":       {"server01"},
			},
		},
		{
			name:  "Multi-value array",
			input: `{"dashboardUid": "abc", "variables": {"var-instance": ["172.16.31.129", "172.16.32.99"]}}`,
			expected: map[string]StringOrSlice{
				"var-instance": {"172.16.31.129", "172.16.32.99"},
			},
		},
		{
			name:  "Mixed single and multi-value",
			input: `{"dashboardUid": "abc", "variables": {"var-datasource": "prometheus", "var-instance": ["server1", "server2"]}}`,
			expected: map[string]StringOrSlice{
				"var-datasource": {"prometheus"},
				"var-instance":   {"server1", "server2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params GetPanelImageParams
			err := json.Unmarshal([]byte(tt.input), &params)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, params.Variables)
		})
	}
}

func TestBuildRenderURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		args        GetPanelImageParams
		contains    []string
		notContains []string
		expectError bool
	}{
		{
			name:    "Basic dashboard render",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
			},
			contains: []string{
				"http://localhost:3000/render/d/abc123",
				"width=1000",
				"height=500",
				"scale=1",
				"kiosk=true",
			},
		},
		{
			name:    "Panel render with custom dimensions uses d-solo path",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				PanelID:      intPtr(5),
				Width:        intPtr(800),
				Height:       intPtr(600),
			},
			contains: []string{
				"http://localhost:3000/render/d-solo/abc123",
				"panelId=5",
				"width=800",
				"height=600",
			},
			notContains: []string{
				"/render/d/abc123",
				"viewPanel=",
			},
		},
		{
			name:    "With time range",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				TimeRange: &RenderTimeRange{
					From: "now-1h",
					To:   "now",
				},
			},
			contains: []string{
				"from=now-1h",
				"to=now",
			},
		},
		{
			name:    "With theme",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				Theme:        stringPtr("light"),
			},
			contains: []string{
				"theme=light",
			},
		},
		{
			name:    "With single-value variables",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				Variables: map[string]StringOrSlice{
					"var-datasource": {"prometheus"},
					"var-host":       {"server01"},
				},
			},
			contains: []string{
				"var-datasource=prometheus",
				"var-host=server01",
			},
		},
		{
			name:    "With multi-value variables",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				Variables: map[string]StringOrSlice{
					"var-instance": {"172.16.31.129", "172.16.32.99"},
				},
			},
			contains: []string{
				"var-instance=172.16.31.129",
				"var-instance=172.16.32.99",
			},
		},
		{
			name:    "With scale",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				Scale:        intPtr(2),
			},
			contains: []string{
				"scale=2",
			},
		},
		{
			name:    "URL with trailing slash",
			baseURL: "http://localhost:3000/",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
			},
			contains: []string{
				"http://localhost:3000/render/d/abc123",
			},
		},
		{
			name:    "Provisioning preview renders the preview path with ref",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "my-repo",
					Path: "folder/dashboard.json",
					Ref:  "feature-branch",
				},
			},
			contains: []string{
				"http://localhost:3000/render/dashboard/provisioning/my-repo/preview/folder/dashboard.json",
				"ref=feature-branch",
				"kiosk=true",
			},
			notContains: []string{
				"/render/d/",
				"/render/d-solo/",
			},
		},
		{
			name:    "Provisioning preview without ref omits the ref query param",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "my-repo",
					Path: "dashboard.json",
				},
			},
			contains: []string{
				"http://localhost:3000/render/dashboard/provisioning/my-repo/preview/dashboard.json",
			},
			notContains: []string{
				"ref=",
			},
		},
		{
			name:    "Provisioning preview escapes special characters in repo and path",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "repo with space",
					Path: "folder/dash?name#frag.json",
				},
			},
			contains: []string{
				"/render/dashboard/provisioning/repo%20with%20space/preview/folder/dash%3Fname%23frag.json",
			},
			notContains: []string{
				"repo with space",
				"dash?name",
				"#frag",
			},
		},
		{
			// url.URL{}.EscapedPath() and url.PathEscape diverge on a few
			// characters that are legal in a path but not in a single segment
			// — notably `;` and `,`. The render URL should encode them the
			// same way navigation/provisioning do (PathEscape semantics) for
			// the single-segment repo slug.
			name:    "Provisioning preview encodes segment-only-reserved chars in repo slug",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "a;b,c",
					Path: "dash.json",
				},
			},
			contains: []string{
				"/render/dashboard/provisioning/a%3Bb%2Cc/preview/dash.json",
			},
			notContains: []string{
				"/provisioning/a;b,c/",
			},
		},
		{
			name:    "Provisioning preview with panel ID uses viewPanel (not d-solo)",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "my-repo",
					Path: "dashboard.json",
					Ref:  "feature-branch",
				},
				PanelID: intPtr(7),
			},
			contains: []string{
				"http://localhost:3000/render/dashboard/provisioning/my-repo/preview/dashboard.json",
				"viewPanel=7",
			},
			notContains: []string{
				"panelId=",
				"/render/d-solo/",
			},
		},
		{
			name:        "Error: neither dashboardUid nor provisioningPreview",
			baseURL:     "http://localhost:3000",
			args:        GetPanelImageParams{},
			expectError: true,
		},
		{
			name:    "Error: both dashboardUid and provisioningPreview",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				DashboardUID: "abc123",
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "my-repo",
					Path: "dashboard.json",
				},
			},
			expectError: true,
		},
		{
			name:    "Error: provisioningPreview missing repo",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Path: "dashboard.json",
				},
			},
			expectError: true,
		},
		{
			name:    "Error: provisioningPreview missing path",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "my-repo",
				},
			},
			expectError: true,
		},
		{
			name:    "Error: provisioningPreview repo with slash",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "evil/../../d",
					Path: "dashboard.json",
				},
			},
			expectError: true,
		},
		{
			name:    "Error: provisioningPreview repo with backslash",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: `evil\..\d`,
					Path: "dashboard.json",
				},
			},
			expectError: true,
		},
		{
			name:    "Error: provisioningPreview repo equals ..",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "..",
					Path: "dashboard.json",
				},
			},
			expectError: true,
		},
		{
			name:    "Error: provisioningPreview path with .. segment",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "my-repo",
					Path: "folder/../../etc/passwd",
				},
			},
			expectError: true,
		},
		{
			name:    "Error: provisioningPreview path is exactly ..",
			baseURL: "http://localhost:3000",
			args: GetPanelImageParams{
				ProvisioningPreview: &ProvisioningPreview{
					Repo: "my-repo",
					Path: "..",
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildRenderURL(tt.baseURL, tt.args)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			for _, expected := range tt.contains {
				assert.Contains(t, result, expected)
			}
			for _, unexpected := range tt.notContains {
				assert.NotContains(t, result, unexpected)
			}
		})
	}
}

func TestGetPanelImage(t *testing.T) {
	// Create a test PNG image (1x1 pixel)
	testPNGData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xFF, 0xFF, 0x3F,
		0x00, 0x05, 0xFE, 0x02, 0xFE, 0xDC, 0xCC, 0x59,
		0xE7, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82, // IEND chunk
	}

	t.Run("Successful image render", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.Path, "/render/d/test-dash")
			assert.Equal(t, "1000", r.URL.Query().Get("width"))
			assert.Equal(t, "500", r.URL.Query().Get("height"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		result, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Content, 1)

		// Check that the content is image content with base64 data
		content := result.Content[0]
		imageContent, ok := content.(interface {
			GetData() string
			GetMimeType() string
		})
		if ok {
			assert.Equal(t, "image/png", imageContent.GetMimeType())
			// Verify base64 decoding works
			decoded, err := base64.StdEncoding.DecodeString(imageContent.GetData())
			require.NoError(t, err)
			assert.Equal(t, testPNGData, decoded)
		}
	})

	t.Run("Panel image with specific panel ID uses d-solo path and panelId param", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.Path, "/render/d-solo/test-dash")
			assert.Equal(t, "5", r.URL.Query().Get("panelId"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		panelID := 5
		result, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
			PanelID:      &panelID,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("Authentication header with API key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
	})

	t.Run("Image renderer not available returns helpful error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "image renderer not available")
		assert.Contains(t, err.Error(), "Grafana Image Renderer service")
	})

	t.Run("Server error returns error message", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Error"))
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 500")
	})

	t.Run("Missing Grafana URL returns error", func(t *testing.T) {
		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL: "",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "grafana URL not configured")
	})

	t.Run("With time range parameters", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "now-1h", r.URL.Query().Get("from"))
			assert.Equal(t, "now", r.URL.Query().Get("to"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
			TimeRange: &RenderTimeRange{
				From: "now-1h",
				To:   "now",
			},
		})

		require.NoError(t, err)
	})

	t.Run("With dashboard variables", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "prometheus", r.URL.Query().Get("var-datasource"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
			Variables: map[string]StringOrSlice{
				"var-datasource": {"prometheus"},
			},
		})

		require.NoError(t, err)
	})

	t.Run("With multi-value dashboard variables", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Multi-value variables should appear as multiple query params
			values := r.URL.Query()["var-instance"]
			assert.ElementsMatch(t, []string{"172.16.31.129", "172.16.32.99"}, values)

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
			Variables: map[string]StringOrSlice{
				"var-instance": {"172.16.31.129", "172.16.32.99"},
			},
		})

		require.NoError(t, err)
	})

	t.Run("Org ID header is set when configured", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "2", r.Header.Get("X-Grafana-Org-Id"))

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
			OrgID:  2,
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
	})

	t.Run("BaseTransport is used when configured", func(t *testing.T) {
		var baseTransportUsed bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
			BaseTransport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				baseTransportUsed = true
				return http.DefaultTransport.RoundTrip(req)
			}),
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
		assert.True(t, baseTransportUsed, "expected BaseTransport to be used as the innermost transport layer")
	})

	t.Run("User-Agent header is set", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.Header.Get("User-Agent"), "mcp-grafana/")

			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(testPNGData)
		}))
		defer server.Close()

		grafanaCfg := mcpgrafana.GrafanaConfig{
			URL:    server.URL,
			APIKey: "test-api-key",
		}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), grafanaCfg)

		_, err := getPanelImage(ctx, GetPanelImageParams{
			DashboardUID: "test-dash",
		})

		require.NoError(t, err)
	})
}
